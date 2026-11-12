package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestTenantPurger_AStopDuringAPurge_IsNotAFailureAndWritesNoBackoffRecord(t *testing.T) {
	t.Parallel()
	tenant := uuid.New()
	retention := &coreSvcARetention{due: []port.TenantPurgeDue{{TenantID: tenant, Artifacts: true, RecordVersion: 4}}}
	failures := 0
	purger := service.NewTenantPurger(service.TenantPurgerDeps{
		Retention: retention, Data: coreSvcAFailingPurger{err: context.Canceled}, Batch: 10, Interval: time.Hour,
		Now:    func() time.Time { return time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC) },
		Record: func(string, int64) {}, Failed: func() { failures++ },
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := purger.Tick(ctx)

	require.Error(t, err, "the stop is still reported to the caller")
	assert.True(t, errors.Is(err, context.Canceled))
	assert.Zero(t, failures, "a stop is not counted as a purge failure, so the failure alert does not fire on a deploy")
	assert.Zero(t, retention.recordCalls, "no backoff record is written on the cancelled context")
}
