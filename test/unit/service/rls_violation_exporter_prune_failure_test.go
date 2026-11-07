package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

type exporterPruneFailureLog struct {
	counts   []domain.RLSViolationCount
	newest   int64
	afterIDs []int64
	pruneErr error
}

func (*exporterPruneFailureLog) LatestID(context.Context) (int64, error) { return 10, nil }

func (l *exporterPruneFailureLog) CountsAfter(_ context.Context, afterID int64) ([]domain.RLSViolationCount, int64, error) {
	l.afterIDs = append(l.afterIDs, afterID)
	counts := l.counts
	l.counts = nil
	if counts == nil {
		return nil, afterID, nil
	}
	return counts, l.newest, nil
}

func (l *exporterPruneFailureLog) PruneBefore(context.Context, time.Time) (int64, error) {
	return 0, l.pruneErr
}

func TestRLSViolationExporter_PruneFails_StillCountedOnceAndErrorIsReported(t *testing.T) {
	pruneErr := errors.New("prune unavailable")
	log := &exporterPruneFailureLog{
		newest:   14,
		counts:   []domain.RLSViolationCount{{ViolationType: "cross_tenant_access", Count: 4}},
		pruneErr: pruneErr,
	}
	type sample struct {
		violationType string
		n             float64
	}
	var recorded []sample
	exp := service.NewRLSViolationExporter(service.RLSViolationExporterDeps{
		Log:       log,
		Record:    func(violationType string, n float64) { recorded = append(recorded, sample{violationType, n}) },
		Retention: 24 * time.Hour,
		Logger:    testkit.FakeLogger{},
	})
	ctx := context.Background()
	require.NoError(t, exp.Start(ctx))

	err := exp.Tick(ctx)

	require.Error(t, err)
	assert.ErrorIs(t, err, pruneErr)
	assert.ErrorContains(t, err, "rls violation prune")
	assert.Equal(t, []sample{{"cross_tenant_access", 4}}, recorded, "the counts were recorded before the prune failed")

	log.pruneErr = nil
	require.NoError(t, exp.Tick(ctx))
	assert.Equal(t, []int64{10, 14}, log.afterIDs, "the next tick starts after the rows already counted")
	assert.Len(t, recorded, 1, "nothing is counted twice")
}
