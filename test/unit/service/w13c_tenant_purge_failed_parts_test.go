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

type w13cBatchThenFailData struct {
	batches []map[string]int64
	err     error
	calls   int
}

func (d *w13cBatchThenFailData) PurgeBatch(context.Context, uuid.UUID, int64, int) (map[string]int64, error) {
	if d.calls >= len(d.batches) {
		return nil, d.err
	}
	d.calls++
	return d.batches[d.calls-1], nil
}

func (d *w13cBatchThenFailData) HasRows(context.Context, uuid.UUID) (bool, error) { return false, nil }

var _ port.TenantDataPurger = (*w13cBatchThenFailData)(nil)

func TestTenantPurger_RowsDeletedBeforeAFailingPartStayCountedAndEachFailingPartCountsOnce(t *testing.T) {
	cases := []struct {
		name        string
		secretsErr  error
		wantFailed  int
		wantCredsOK bool
	}{
		{"only the artifacts part fails", nil, 1, true},
		{"credentials and artifacts both fail in one tenant", errors.New("openbao down"), 2, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant := uuid.New()
			r := &fakeRetention{due: bothDue(tenant)}
			d := &w13cBatchThenFailData{
				batches: []map[string]int64{{"workflow": 50}, {"workflow": 3, "workflow_module": 1}},
				err:     errors.New("fk violation"),
			}
			s := &fakeSecrets{err: tc.secretsErr}
			rec, failed := map[string]int64{}, 0
			purger := service.NewTenantPurger(service.TenantPurgerDeps{
				Retention: r, Data: d, Secrets: s, Batch: 50, Interval: time.Hour,
				Now:    func() time.Time { return time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC) },
				Record: func(table string, n int64) { rec[table] += n },
				Failed: func() { failed++ },
			})

			err := purger.Tick(context.Background())

			require.Error(t, err)
			assert.Equal(t, tc.wantFailed, failed, "one count per failing part, so at most two for one tenant")
			assert.Equal(t, map[string]int64{"workflow": 53, "workflow_module": 1}, rec, "the rows deleted before the failure stay counted")
			assert.Empty(t, r.purged, "a tenant whose data purge failed is not marked purged")
			assert.Equal(t, tc.wantCredsOK, len(r.credsDone) == 1)
			require.Len(t, r.failures, 1, "one retry record per tenant, however many parts failed")
			assert.Equal(t, tenant, r.failures[0].tenantID)
		})
	}
}
