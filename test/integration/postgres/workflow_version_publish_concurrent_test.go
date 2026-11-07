//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestWorkflowVersionRepo_Publish_ConcurrentCallsForOneDraft_OnePublishesTheRestAreRefused(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	tenantID := uuid.New()
	_, draft := publishRaceSeedDraft(t, pool, tenantID, "pub-concurrent-repo", "<definitions/>")
	read, err := repo.GetByID(ctx, tenantID, draft.ID)
	require.NoError(t, err)

	const callers = 8
	errs := make([]error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = repo.Publish(ctx, tenantID, draft.ID, read.RecordVersion, 1, `{"steps":[]}`, fmt.Sprintf("hash-%d", i), nil)
		}()
	}
	close(start)
	wg.Wait()

	winner := -1
	for i, err := range errs {
		if err == nil {
			require.Equal(t, -1, winner, "a second publish also succeeded: %v", errs)
			winner = i
			continue
		}
		assert.True(t, errors.Is(err, domain.ErrVersionNotDraft), "caller %d got %v, want ErrVersionNotDraft", i, err)
	}
	require.NotEqual(t, -1, winner, "no publish succeeded: %v", errs)

	got, err := repo.GetByID(ctx, tenantID, draft.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.VersionStatusPublished, got.Status)
	require.NotNil(t, got.VersionNumber)
	assert.EqualValues(t, 1, *got.VersionNumber)
	assert.Equal(t, fmt.Sprintf("hash-%d", winner), got.ArtifactHash, "the stored publish is the winner's, not a mix")
}
