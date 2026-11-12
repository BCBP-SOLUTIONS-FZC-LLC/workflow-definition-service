package service_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type w5bRetention struct {
	port.TenantRetentionRepository
	due     []port.TenantPurgeDue
	listErr error
	listed  int
}

func (r *w5bRetention) ListDue(context.Context, time.Time, int) ([]port.TenantPurgeDue, error) {
	r.listed++
	return r.due, r.listErr
}

type w5bPurgeData struct {
	mu    sync.Mutex
	calls []string
}

func (d *w5bPurgeData) PurgeBatch(context.Context, uuid.UUID, int64, int) (map[string]int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, "PurgeBatch")
	return map[string]int64{}, nil
}

func (d *w5bPurgeData) HasRows(context.Context, uuid.UUID) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, "HasRows")
	return false, nil
}

type w5bPurgeSecrets struct {
	port.SecretsClient
	mu      sync.Mutex
	deleted []string
}

func (s *w5bPurgeSecrets) Delete(_ context.Context, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, path)
	return nil
}

func TestTenantPurger_Tick_ListDueFails_ReturnsWrappedErrorAndPurgesNothing(t *testing.T) {
	t.Parallel()

	cause := errors.New("retention table unreachable")
	retention := &w5bRetention{
		due:     []port.TenantPurgeDue{{TenantID: uuid.New(), Credentials: true, Artifacts: true, RecordVersion: 1}},
		listErr: cause,
	}
	data := &w5bPurgeData{}
	secrets := &w5bPurgeSecrets{}
	var recorded, failed int
	purger := service.NewTenantPurger(service.TenantPurgerDeps{
		Retention: retention, Data: data, Secrets: secrets, Batch: 10, Interval: time.Hour,
		Now:    func() time.Time { return time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC) },
		Record: func(string, int64) { recorded++ },
		Failed: func() { failed++ },
	})

	err := purger.Tick(context.Background())

	require.Error(t, err)
	assert.ErrorIs(t, err, cause)
	assert.True(t, strings.HasPrefix(err.Error(), "list tenants due for purge: "), "err = %q", err)
	assert.Equal(t, 1, retention.listed)
	assert.Empty(t, data.calls, "no tenant data is touched")
	assert.Empty(t, secrets.deleted, "no credential is deleted")
	assert.Zero(t, recorded)
	assert.Zero(t, failed, "a failed listing is not counted as a failed tenant purge")
}
