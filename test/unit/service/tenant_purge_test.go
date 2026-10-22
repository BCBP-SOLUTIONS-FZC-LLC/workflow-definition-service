package service_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type fakeRetention struct {
	due       []port.TenantPurgeDue
	listErr   error
	listedAt  time.Time
	onList    func()
	purged    []uuid.UUID
	credsDone []uuid.UUID
	markErr   error
	credsErr  error
	recorded  []recordedOffboard
	recordErr error
}

type recordedOffboard struct {
	tenantID         uuid.UUID
	offboardedAt     time.Time
	credentialsAfter time.Time
	artifactsAfter   time.Time
}

func (f *fakeRetention) RecordOffboarded(_ context.Context, tenantID uuid.UUID, offboardedAt, credentialsAfter, artifactsAfter time.Time) error {
	f.recorded = append(f.recorded, recordedOffboard{tenantID, offboardedAt, credentialsAfter, artifactsAfter})
	return f.recordErr
}

func (f *fakeRetention) ListDue(_ context.Context, now time.Time, _ int) ([]port.TenantPurgeDue, error) {
	f.listedAt = now
	if f.onList != nil {
		f.onList()
	}
	return f.due, f.listErr
}

func (f *fakeRetention) MarkCredentialsPurged(_ context.Context, tenantID uuid.UUID, _ time.Time) error {
	if f.credsErr != nil {
		return f.credsErr
	}
	f.credsDone = append(f.credsDone, tenantID)
	return nil
}

func (f *fakeRetention) MarkPurged(_ context.Context, tenantID uuid.UUID, _ time.Time) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.purged = append(f.purged, tenantID)
	return nil
}

func bothDue(ids ...uuid.UUID) []port.TenantPurgeDue {
	var out []port.TenantPurgeDue
	for _, id := range ids {
		out = append(out, port.TenantPurgeDue{TenantID: id, Credentials: true, Artifacts: true})
	}
	return out
}

type fakeDataPurger struct {
	script  map[uuid.UUID][]map[string]int64
	errFor  map[uuid.UUID]error
	calls   map[uuid.UUID]int
	rows    map[uuid.UUID]bool
	rowsErr error
}

func (f *fakeDataPurger) HasRows(_ context.Context, tenantID uuid.UUID) (bool, error) {
	return f.rows[tenantID], f.rowsErr
}

func (f *fakeDataPurger) PurgeBatch(_ context.Context, tenantID uuid.UUID, _ int) (map[string]int64, error) {
	if f.calls == nil {
		f.calls = map[uuid.UUID]int{}
	}
	if err := f.errFor[tenantID]; err != nil {
		return nil, err
	}
	i := f.calls[tenantID]
	f.calls[tenantID]++
	steps := f.script[tenantID]
	if i >= len(steps) {
		return map[string]int64{}, nil
	}
	return steps[i], nil
}

type fakeSecrets struct {
	deleted []string
	err     error
	order   *[]string
}

func (f *fakeSecrets) Write(context.Context, string, map[string]string) error { return nil }

func (f *fakeSecrets) Delete(_ context.Context, path string) error {
	if f.order != nil {
		*f.order = append(*f.order, "secret")
	}
	f.deleted = append(f.deleted, path)
	return f.err
}

func newPurger(r *fakeRetention, d *fakeDataPurger, s *fakeSecrets, rec map[string]int64, failed *int) *service.TenantPurger {
	deps := service.TenantPurgerDeps{
		Retention: r, Data: d, Batch: 50, Interval: time.Hour,
		Now:    func() time.Time { return time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC) },
		Record: func(table string, n int64) { rec[table] += n },
		Failed: func() { *failed++ },
	}
	if s != nil {
		deps.Secrets = s
	}
	return service.NewTenantPurger(deps)
}

func TestTenantPurger_DeletesTheCredentialsTheRegistryDefinesThenThePlanningDataThenMarks(t *testing.T) {
	tenant := uuid.New()
	r := &fakeRetention{due: bothDue(tenant)}
	d := &fakeDataPurger{script: map[uuid.UUID][]map[string]int64{tenant: {{"workflow": 50}, {"workflow": 3}, {"workflow_module": 1}}}}
	s := &fakeSecrets{}
	rec, failed := map[string]int64{}, 0

	require.NoError(t, newPurger(r, d, s, rec, &failed).Tick(context.Background()))

	assert.Equal(t, []uuid.UUID{tenant}, r.purged)
	assert.Equal(t, map[string]int64{"workflow": 53, "workflow_module": 1}, rec)
	assert.NotEmpty(t, s.deleted)
	sort.Strings(s.deleted)
	for _, p := range s.deleted {
		assert.Contains(t, p, "connectors/"+tenant.String()+"/", "only this tenant's credential paths are deleted")
	}
	assert.Contains(t, s.deleted, "connectors/"+tenant.String()+"/send-email/apiKey")
	assert.Zero(t, failed)
}

func TestTenantPurger_ACredentialFailureLeavesTheCredentialsDueAndDoesNotBlockTheWorkflows(t *testing.T) {
	tenant := uuid.New()
	r := &fakeRetention{due: bothDue(tenant)}
	d := &fakeDataPurger{script: map[uuid.UUID][]map[string]int64{tenant: {{"workflow": 1}}}}
	s := &fakeSecrets{err: errors.New("openbao down")}
	rec, failed := map[string]int64{}, 0

	require.Error(t, newPurger(r, d, s, rec, &failed).Tick(context.Background()))

	assert.Empty(t, r.credsDone, "the credentials stay due, so the next run retries them")
	assert.Equal(t, []uuid.UUID{tenant}, r.purged, "the workflows have their own period and their own mark")
	assert.Equal(t, 1, failed)
}

func TestTenantPurger_OnlyThePartThatIsDueIsPurged(t *testing.T) {
	credsOnly, artifactsOnly := uuid.New(), uuid.New()
	r := &fakeRetention{due: []port.TenantPurgeDue{{TenantID: credsOnly, Credentials: true}, {TenantID: artifactsOnly, Artifacts: true}}}
	d := &fakeDataPurger{}
	s := &fakeSecrets{}
	rec, failed := map[string]int64{}, 0

	require.NoError(t, newPurger(r, d, s, rec, &failed).Tick(context.Background()))

	assert.Equal(t, []uuid.UUID{credsOnly}, r.credsDone)
	assert.Equal(t, []uuid.UUID{artifactsOnly}, r.purged)
	assert.NotContains(t, d.calls, credsOnly, "a tenant whose workflows are not due keeps them")
	for _, p := range s.deleted {
		assert.Contains(t, p, credsOnly.String(), "no credential of the other tenant is touched")
	}
}

func TestTenantPurger_ATenantWithRowsLeftIsNotMarkedPurged(t *testing.T) {
	tenant := uuid.New()
	r := &fakeRetention{due: bothDue(tenant)}
	d := &fakeDataPurger{rows: map[uuid.UUID]bool{tenant: true}}
	rec, failed := map[string]int64{}, 0

	err := newPurger(r, d, nil, rec, &failed).Tick(context.Background())

	require.Error(t, err, "a batch that deleted nothing can mean another replica took the rows, not that none are left")
	assert.Empty(t, r.purged)
	assert.Equal(t, 1, failed)
}

func TestTenantPurger_AFailureToCheckForRemainingRowsIsReturned(t *testing.T) {
	tenant := uuid.New()
	r := &fakeRetention{due: bothDue(tenant)}
	d := &fakeDataPurger{rowsErr: errors.New("db down")}
	rec, failed := map[string]int64{}, 0
	require.Error(t, newPurger(r, d, nil, rec, &failed).Tick(context.Background()))
	assert.Empty(t, r.purged)
}

func TestTenantPurger_WithoutAVaultTheCredentialStepIsSkipped(t *testing.T) {
	tenant := uuid.New()
	r := &fakeRetention{due: bothDue(tenant)}
	rec, failed := map[string]int64{}, 0

	require.NoError(t, newPurger(r, &fakeDataPurger{}, nil, rec, &failed).Tick(context.Background()))
	assert.Equal(t, []uuid.UUID{tenant}, r.purged)
}

func TestTenantPurger_AFailingTenantDoesNotStopTheOthers(t *testing.T) {
	bad, good := uuid.New(), uuid.New()
	r := &fakeRetention{due: bothDue(bad, good)}
	d := &fakeDataPurger{errFor: map[uuid.UUID]error{bad: errors.New("fk violation")}}
	rec, failed := map[string]int64{}, 0

	require.Error(t, newPurger(r, d, &fakeSecrets{}, rec, &failed).Tick(context.Background()))
	assert.Equal(t, []uuid.UUID{good}, r.purged)
	assert.Equal(t, 1, failed)
}

func TestTenantPurger_NothingDueAndListFailure(t *testing.T) {
	rec, failed := map[string]int64{}, 0
	require.NoError(t, newPurger(&fakeRetention{}, &fakeDataPurger{}, nil, rec, &failed).Tick(context.Background()))
	require.Error(t, newPurger(&fakeRetention{listErr: errors.New("db down")}, &fakeDataPurger{}, nil, rec, &failed).Tick(context.Background()))
}

func TestTenantPurger_ACancelledRunLeavesTheTenantUnmarked(t *testing.T) {
	tenant := uuid.New()
	r := &fakeRetention{due: bothDue(tenant)}
	d := &fakeDataPurger{script: map[uuid.UUID][]map[string]int64{tenant: {{"workflow": 50}}}}
	rec, failed := map[string]int64{}, 0
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.Error(t, newPurger(r, d, nil, rec, &failed).Tick(ctx))
	assert.Empty(t, r.purged)
}

func TestTenantPurger_AFailureToMarkIsReturned(t *testing.T) {
	tenant := uuid.New()
	r := &fakeRetention{due: bothDue(tenant), markErr: errors.New("db down")}
	rec, failed := map[string]int64{}, 0
	require.Error(t, newPurger(r, &fakeDataPurger{}, nil, rec, &failed).Tick(context.Background()))
	assert.Equal(t, 1, failed)
}

func TestTenantPurger_RunSweepsAndStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sweeps := 0
	r := &fakeRetention{onList: func() { sweeps++; cancel() }, listErr: errors.New("db down")}
	rec, failed := map[string]int64{}, 0
	purger := newPurger(r, &fakeDataPurger{}, nil, rec, &failed)

	done := make(chan struct{})
	go func() { purger.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after its context was cancelled")
	}
	assert.Equal(t, 1, sweeps)
}

func TestTenantPurger_DefaultsTheClockAndLoggerWhenNoneAreGiven(t *testing.T) {
	tenant := uuid.New()
	r := &fakeRetention{due: bothDue(tenant)}
	p := service.NewTenantPurger(service.TenantPurgerDeps{
		Retention: r, Data: &fakeDataPurger{}, Batch: 10, Interval: time.Hour,
		Record: func(string, int64) {}, Failed: func() {},
	})
	require.NoError(t, p.Tick(context.Background()))
	assert.WithinDuration(t, time.Now(), r.listedAt, time.Minute)
}

func TestTenantOffboardingService_RecordsBothPurgeDatesAfterTheirOwnRetention(t *testing.T) {
	r := &fakeRetention{}
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	at := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	svc := service.NewTenantOffboardingService(service.TenantOffboardingDeps{
		Retention: r, CredentialRetention: 72 * time.Hour, ArtifactRetention: 240 * time.Hour, Now: func() time.Time { return now },
	})
	tenant := uuid.New()

	require.NoError(t, svc.RecordOffboarded(context.Background(), tenant, at))
	require.NoError(t, svc.RecordOffboarded(context.Background(), tenant, time.Time{}))

	require.Len(t, r.recorded, 2)
	assert.Equal(t, recordedOffboard{tenant, at, at.Add(72 * time.Hour), at.Add(240 * time.Hour)}, r.recorded[0], "the event time anchors both periods")
	assert.Equal(t, recordedOffboard{tenant, now, now.Add(72 * time.Hour), now.Add(240 * time.Hour)}, r.recorded[1], "an event without a time is anchored at receipt")
}

func TestTenantOffboardingService_ARecordFailureIsReturned(t *testing.T) {
	r := &fakeRetention{recordErr: errors.New("db down")}
	svc := service.NewTenantOffboardingService(service.TenantOffboardingDeps{Retention: r, CredentialRetention: time.Hour, ArtifactRetention: time.Hour})
	require.Error(t, svc.RecordOffboarded(context.Background(), uuid.New(), time.Now()))
}
