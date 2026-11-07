package service_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type coreSvcARetention struct {
	port.TenantRetentionRepository
	due           []port.TenantPurgeDue
	recordFailErr error
	recordCalls   int
}

func (r *coreSvcARetention) ListDue(context.Context, time.Time, int) ([]port.TenantPurgeDue, error) {
	return r.due, nil
}

func (r *coreSvcARetention) RecordFailure(context.Context, uuid.UUID, int64, int, time.Time) error {
	r.recordCalls++
	return r.recordFailErr
}

type coreSvcAFailingPurger struct {
	err error
}

func (p coreSvcAFailingPurger) PurgeBatch(context.Context, uuid.UUID, int64, int) (map[string]int64, error) {
	return nil, p.err
}

func (coreSvcAFailingPurger) HasRows(context.Context, uuid.UUID) (bool, error) { return false, nil }

func TestTenantPurger_RecordFailureFails_AddsItsErrorUnlessTheRecordChanged(t *testing.T) {
	t.Parallel()

	ledgerDown := errors.New("ledger write timed out")
	cases := []struct {
		name          string
		recordFailErr error
		wantRecordErr bool
		wantErrCount  int
	}{
		{name: "an unrelated failure of the bookkeeping write is returned with the purge error", recordFailErr: ledgerDown, wantRecordErr: true, wantErrCount: 2},
		{name: "a changed record during the bookkeeping write is swallowed", recordFailErr: fmt.Errorf("record: %w", domain.ErrTenantRetentionChanged), wantErrCount: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tenant := uuid.New()
			purgeErr := errors.New("fk violation")
			retention := &coreSvcARetention{
				due:           []port.TenantPurgeDue{{TenantID: tenant, Artifacts: true, RecordVersion: 4}},
				recordFailErr: tc.recordFailErr,
			}
			purger := service.NewTenantPurger(service.TenantPurgerDeps{
				Retention: retention, Data: coreSvcAFailingPurger{err: purgeErr}, Batch: 10, Interval: time.Hour,
				Now:    func() time.Time { return time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC) },
				Record: func(string, int64) {}, Failed: func() {},
			})

			err := purger.Tick(context.Background())

			require.Error(t, err)
			assert.Equal(t, 1, retention.recordCalls)
			assert.ErrorIs(t, err, purgeErr, "the original purge error is always kept")
			assert.Equal(t, tc.wantRecordErr, errors.Is(err, ledgerDown))
			assert.Equal(t, tc.wantRecordErr, strings.Contains(err.Error(), fmt.Sprintf("record tenant %s purge failure", tenant)))
			assert.False(t, errors.Is(err, domain.ErrTenantRetentionChanged))
			joined, ok := err.(interface{ Unwrap() []error })
			require.True(t, ok)
			assert.Len(t, joined.Unwrap(), tc.wantErrCount)
		})
	}
}
