package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

// stopRetention records the failure writes and whether their context was
// already cancelled when they ran.
type stopRetention struct {
	fakeRetention
	failCtxErrs []error
}

func (r *stopRetention) RecordFailure(ctx context.Context, tenantID uuid.UUID, version int64, attempts int, next time.Time) error {
	r.failCtxErrs = append(r.failCtxErrs, ctx.Err())
	return r.fakeRetention.RecordFailure(ctx, tenantID, version, attempts, next)
}

type purgeLog struct {
	mu     sync.Mutex
	errors []string
}

func (l *purgeLog) Debug(string, map[string]any) {}
func (l *purgeLog) Info(string, map[string]any)  {}
func (l *purgeLog) Warn(string, map[string]any)  {}
func (l *purgeLog) Fatal(string, map[string]any) {}
func (l *purgeLog) Error(msg string, _ map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, msg)
}

func (l *purgeLog) errorLines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.errors...)
}

// cancellingSecrets fails every delete with a real error and cancels the
// context, as a shutdown that arrives while the credentials part is failing.
type cancellingSecrets struct {
	fakeSecrets
	cancel context.CancelFunc
}

func (s *cancellingSecrets) Delete(ctx context.Context, path string) error {
	s.cancel()
	return s.fakeSecrets.Delete(ctx, path)
}

func TestTenantPurger_ARealCredentialFailureIsRecordedWhenTheArtifactsPartIsStoppedByShutdown(t *testing.T) {
	tenant := uuid.New()
	retention := &stopRetention{fakeRetention: fakeRetention{due: bothDue(tenant)}}
	ctx, cancel := context.WithCancel(context.Background())
	secrets := &cancellingSecrets{fakeSecrets: fakeSecrets{err: errors.New("vault returned 500")}, cancel: cancel}
	failed := 0
	purger := service.NewTenantPurger(service.TenantPurgerDeps{
		Retention: retention, Data: &fakeDataPurger{}, Secrets: secrets, Batch: 10, Interval: time.Hour,
		Now:    func() time.Time { return time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC) },
		Record: func(string, int64) {}, Failed: func() { failed++ },
	})

	err := purger.Tick(ctx)

	require.Error(t, err)
	assert.ErrorContains(t, err, "vault returned 500", "the real failure is returned")
	assert.ErrorIs(t, err, context.Canceled, "the stop is returned too")
	assert.Equal(t, 1, failed, "the credential failure is counted once, the stop is not")
	require.Len(t, retention.failures, 1, "the real failure reaches the backoff record")
	assert.Equal(t, tenant, retention.failures[0].tenantID)
	assert.Equal(t, 1, retention.failures[0].attempts)
	assert.NoError(t, retention.failCtxErrs[0], "the write must not run on the cancelled context")
}

func TestTenantPurger_AStopWithNoRealFailureWritesNoRecord(t *testing.T) {
	tenant := uuid.New()
	retention := &stopRetention{fakeRetention: fakeRetention{due: bothDue(tenant)}}
	failed := 0
	purger := service.NewTenantPurger(service.TenantPurgerDeps{
		Retention: retention, Data: &fakeDataPurger{}, Batch: 10, Interval: time.Hour,
		Record: func(string, int64) {}, Failed: func() { failed++ },
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := purger.Tick(ctx)

	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, failed)
	assert.Empty(t, retention.failures)
}

func TestTenantPurger_ListDueFailure_CallsFailed(t *testing.T) {
	failed := 0
	purger := service.NewTenantPurger(service.TenantPurgerDeps{
		Retention: &fakeRetention{listErr: errors.New("retention table unreachable")},
		Data:      &fakeDataPurger{}, Batch: 10, Interval: time.Hour,
		Record: func(string, int64) {}, Failed: func() { failed++ },
	})

	require.Error(t, purger.Tick(context.Background()))

	assert.Equal(t, 1, failed, "a sweep that cannot list the due tenants is a purge failure")
}

func TestTenantPurger_ListDueStoppedByShutdown_IsNotCounted(t *testing.T) {
	failed := 0
	purger := service.NewTenantPurger(service.TenantPurgerDeps{
		Retention: &fakeRetention{listErr: context.Canceled},
		Data:      &fakeDataPurger{}, Batch: 10, Interval: time.Hour,
		Record: func(string, int64) {}, Failed: func() { failed++ },
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.ErrorIs(t, purger.Tick(ctx), context.Canceled)

	assert.Zero(t, failed)
}

func TestTenantPurger_Run_LogsAFailedSweepButNotTheStopAtShutdown(t *testing.T) {
	cases := []struct {
		name       string
		listErr    error
		cancelOn   bool
		wantErrors int
	}{
		{name: "a real failure while running is logged", listErr: errors.New("db down"), wantErrors: 1},
		{name: "the context cancel at shutdown is not logged as a failed sweep", listErr: context.Canceled, cancelOn: true, wantErrors: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := &purgeLog{}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			retention := &fakeRetention{listErr: tc.listErr}
			retention.onList = func() {
				if tc.cancelOn {
					cancel()
				}
			}
			purger := service.NewTenantPurger(service.TenantPurgerDeps{
				Retention: retention, Data: &fakeDataPurger{}, Batch: 10, Interval: time.Hour,
				Record: func(string, int64) {}, Failed: func() {}, Logger: log,
			})
			done := make(chan struct{})
			go func() { purger.Run(ctx); close(done) }()
			if !tc.cancelOn {
				require.Eventually(t, func() bool { return len(log.errorLines()) > 0 }, 5*time.Second, time.Millisecond)
				cancel()
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not stop")
			}

			assert.Len(t, log.errorLines(), tc.wantErrors)
		})
	}
}

var _ port.TenantRetentionRepository = (*stopRetention)(nil)
