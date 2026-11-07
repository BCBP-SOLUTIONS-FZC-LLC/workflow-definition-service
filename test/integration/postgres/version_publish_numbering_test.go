//go:build integration

package postgres_test

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

// coreSvcBOverlapVersions holds the first two NextVersionNumber calls until
// both have been made, so both publish transactions read the same maximum
// before either writes. Later calls (the retry of the loser) pass straight
// through.
type coreSvcBOverlapVersions struct {
	port.WorkflowVersionRepository
	calls   atomic.Int32
	arrived chan struct{}
	release chan struct{}
	once    sync.Once
}

func coreSvcBNewOverlapVersions(inner port.WorkflowVersionRepository) *coreSvcBOverlapVersions {
	return &coreSvcBOverlapVersions{
		WorkflowVersionRepository: inner,
		arrived:                   make(chan struct{}, 2),
		release:                   make(chan struct{}),
	}
}

func (o *coreSvcBOverlapVersions) NextVersionNumber(ctx context.Context, tenantID, workflowID uuid.UUID) (int32, error) {
	n, err := o.WorkflowVersionRepository.NextVersionNumber(ctx, tenantID, workflowID)
	if o.calls.Add(1) > 2 {
		return n, err
	}
	o.arrived <- struct{}{}
	if len(o.arrived) == 2 {
		o.once.Do(func() { close(o.release) })
	}
	select {
	case <-o.release:
	case <-ctx.Done():
	case <-time.After(15 * time.Second):
	}
	return n, err
}

func TestVersionService_Publish_TwoDraftsOfOneWorkflowReadingTheSameNumber_GetDistinctNumbers(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	bpmnXML, err := os.ReadFile("../../unit/service/testdata/single-process.bpmn")
	require.NoError(t, err)
	tenantID, userID := uuid.New(), uuid.New()
	wf, first := publishRaceSeedDraft(t, pool, tenantID, "pub-numbering", string(bpmnXML))

	// The schema allows one draft per workflow; lift that in this throwaway
	// database so a second draft of the same workflow can be published beside
	// the first and only the serializable transaction keeps their numbers apart.
	require.NoError(t, pool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx, `DROP INDEX idx_wv_single_draft`)
		return err
	}))
	second := newDraftVersion(wf.ID, tenantID)
	second.BPMNXML = string(bpmnXML)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	require.NoError(t, vRepo.Create(ctx, second))

	overlap := coreSvcBNewOverlapVersions(vRepo)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor:     postgres.NewTransactor(pool),
		Workflows:      postgres.NewWorkflowRepo(pool),
		Versions:       overlap,
		Assignees:      postgres.NewAssigneeRepo(pool),
		Compiler:       bpmn_compiler.New(),
		ModuleVersions: postgres.NewModuleVersionRepo(pool),
	})

	drafts := []*domain.WorkflowVersion{first, second}
	published := make([]*domain.WorkflowVersion, len(drafts))
	errs := make([]error, len(drafts))
	var wg sync.WaitGroup
	for i, d := range drafts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			published[i], errs[i] = svc.Publish(ctx, tenantID, userID, wf.ID, d.ID, true)
		}()
	}
	wg.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	assert.GreaterOrEqual(t, int(overlap.calls.Load()), 3, "the losing transaction was not retried")

	numbers := map[int32]uuid.UUID{}
	for i, d := range drafts {
		stored, err := vRepo.GetByID(ctx, tenantID, d.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.VersionStatusPublished, stored.Status)
		require.NotNil(t, stored.VersionNumber)
		require.NotNil(t, published[i].VersionNumber)
		assert.Equal(t, *stored.VersionNumber, *published[i].VersionNumber, "the number returned is the number stored")
		numbers[*stored.VersionNumber] = d.ID
	}
	assert.Len(t, numbers, 2, "both drafts were given the same number")
	assert.Contains(t, numbers, int32(1))
	assert.Contains(t, numbers, int32(2))

	reloaded, err := postgres.NewWorkflowRepo(pool).GetByID(ctx, tenantID, wf.ID)
	require.NoError(t, err)
	require.NotNil(t, reloaded.ActiveVersionID)
	assert.Equal(t, numbers[2], *reloaded.ActiveVersionID, "the active version is the one numbered last")
}
