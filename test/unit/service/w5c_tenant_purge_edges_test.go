package service_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/v2/pkg/registry"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

var w5cPurgeNow = time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)

type w5cCurrentCall struct {
	tenantID uuid.UUID
	version  int64
}

type w5cRetention struct {
	fakeRetention
	currentErr    func(call int) error
	currentCalls  []w5cCurrentCall
	markPurgedErr map[uuid.UUID]error
}

func (r *w5cRetention) Current(_ context.Context, tenantID uuid.UUID, version int64) error {
	r.currentCalls = append(r.currentCalls, w5cCurrentCall{tenantID, version})
	if r.currentErr != nil {
		return r.currentErr(len(r.currentCalls))
	}
	return nil
}

func (r *w5cRetention) MarkPurged(ctx context.Context, tenantID uuid.UUID, version int64, at time.Time) (int64, error) {
	if err := r.markPurgedErr[tenantID]; err != nil {
		return 0, err
	}
	return r.fakeRetention.MarkPurged(ctx, tenantID, version, at)
}

type w5cLog struct {
	testkit.FakeLogger
	mu     sync.Mutex
	infos  []string
	errors []string
}

func (l *w5cLog) Info(msg string, _ map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infos = append(l.infos, msg)
}

func (l *w5cLog) Error(msg string, _ map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, msg)
}

type w5cSecrets struct {
	deleted []string
	failOn  string
	cause   error
}

func (s *w5cSecrets) Write(context.Context, string, map[string]string) error { return nil }

func (s *w5cSecrets) Delete(_ context.Context, path string) error {
	s.deleted = append(s.deleted, path)
	if path == s.failOn {
		return s.cause
	}
	return nil
}

type w5cPurgeRig struct {
	purger *service.TenantPurger
	failed *int
	log    *w5cLog
}

func w5cNewPurger(r port.TenantRetentionRepository, d port.TenantDataPurger, s port.SecretsClient, interval time.Duration) w5cPurgeRig {
	failed := 0
	log := &w5cLog{}
	return w5cPurgeRig{
		purger: service.NewTenantPurger(service.TenantPurgerDeps{
			Retention: r, Data: d, Secrets: s, Batch: 50, Interval: interval,
			Now:    func() time.Time { return w5cPurgeNow },
			Record: func(string, int64) {}, Failed: func() { failed++ },
			Logger: log,
		}),
		failed: &failed,
		log:    log,
	}
}

func w5cSecretPaths(t *testing.T, tenant uuid.UUID) []string {
	t.Helper()
	var paths []string
	for connectorType, def := range registry.All() {
		for _, f := range def.Inputs {
			if f.IsSecretRef() {
				paths = append(paths, fmt.Sprintf("connectors/%s/%s/%s", tenant, connectorType, f.Name))
			}
		}
	}
	slices.Sort(paths)
	require.GreaterOrEqual(t, len(paths), 3, "the registry defines several secret fields")
	return paths
}

func TestTenantPurger_RecordChangedUnderEitherStep_SkipsThatTenantQuietlyAndContinuesWithTheNext(t *testing.T) {
	cases := []struct {
		name           string
		credentials    bool
		wantCredsDone  bool
		wantSecretsGot bool
	}{
		{name: "the artifacts step alone meets the change", credentials: false},
		{name: "the artifacts step meets the change after the credentials step finished", credentials: true, wantCredsDone: true, wantSecretsGot: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed, other := uuid.New(), uuid.New()
			r := &w5cRetention{markPurgedErr: map[uuid.UUID]error{changed: fmt.Errorf("mark: %w", domain.ErrTenantRetentionChanged)}}
			r.due = []port.TenantPurgeDue{
				{TenantID: changed, Credentials: tc.credentials, Artifacts: true, RecordVersion: 1},
				{TenantID: other, Artifacts: true, RecordVersion: 1},
			}
			secrets := &w5cSecrets{}
			rig := w5cNewPurger(r, &fakeDataPurger{}, secrets, time.Hour)

			err := rig.purger.Tick(context.Background())

			require.NoError(t, err)
			assert.Zero(t, *rig.failed, "a changed record is not a failure")
			assert.Empty(t, r.failures, "no failure is recorded against a record that moved on")
			assert.Empty(t, rig.log.errors)
			assert.Equal(t, []string{"tenant purge skipped: its record changed", "tenant purged"}, rig.log.infos)
			assert.Equal(t, []uuid.UUID{other}, r.purged, "the tenant after the changed one is still purged")
			assert.Equal(t, tc.wantCredsDone, slices.Contains(r.credsDone, changed))
			assert.Equal(t, tc.wantSecretsGot, len(secrets.deleted) > 0)
		})
	}
}

func TestTenantPurger_FailureAfterASuccessfulPart_WaitsOneIntervalAndABothFailWaitsLonger(t *testing.T) {
	cases := []struct {
		name        string
		due         func(tenant uuid.UUID) port.TenantPurgeDue
		secretsFail bool
		artifacts   bool
		wantFailed  int
		wantRecord  func(tenant uuid.UUID) recordedFailure
	}{
		{
			name:       "credentials done then artifacts fail restarts the count",
			due:        func(id uuid.UUID) port.TenantPurgeDue { return bothDue(id)[0] },
			artifacts:  true,
			wantFailed: 1,
			wantRecord: func(id uuid.UUID) recordedFailure { return recordedFailure{id, 2, 1, w5cPurgeNow.Add(time.Hour)} },
		},
		{
			name:        "credentials fail then artifacts done restarts the count",
			due:         func(id uuid.UUID) port.TenantPurgeDue { return bothDue(id)[0] },
			secretsFail: true,
			wantFailed:  1,
			wantRecord:  func(id uuid.UUID) recordedFailure { return recordedFailure{id, 2, 1, w5cPurgeNow.Add(time.Hour)} },
		},
		{
			name:        "both parts failing keeps counting and doubles the wait",
			due:         func(id uuid.UUID) port.TenantPurgeDue { return bothDue(id)[0] },
			secretsFail: true,
			artifacts:   true,
			wantFailed:  2,
			wantRecord:  func(id uuid.UUID) recordedFailure { return recordedFailure{id, 1, 4, w5cPurgeNow.Add(8 * time.Hour)} },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant := uuid.New()
			r := &w5cRetention{}
			due := tc.due(tenant)
			due.Attempts = 3
			r.due = []port.TenantPurgeDue{due}
			d := &fakeDataPurger{}
			if tc.artifacts {
				d.errFor = map[uuid.UUID]error{tenant: errors.New("fk violation")}
			}
			secrets := &w5cSecrets{}
			if tc.secretsFail {
				secrets.failOn = w5cSecretPaths(t, tenant)[0]
				secrets.cause = errors.New("openbao down")
			}
			rig := w5cNewPurger(r, d, secrets, time.Hour)

			require.Error(t, rig.purger.Tick(context.Background()))

			assert.Equal(t, tc.wantFailed, *rig.failed)
			assert.Equal(t, []recordedFailure{tc.wantRecord(tenant)}, r.failures)
		})
	}
}

func TestTenantPurger_MarkFailsAfterEveryRowIsDeleted_TheNextTickFindsNoRowsAndRetriesTheMark(t *testing.T) {
	tenant := uuid.New()
	r := &w5cRetention{}
	r.due = []port.TenantPurgeDue{{TenantID: tenant, Artifacts: true, RecordVersion: 1}}
	r.markErr = errors.New("db down")
	d := &fakeDataPurger{script: map[uuid.UUID][]map[string]int64{tenant: {{"workflow": 3}}}}
	rec := map[string]int64{}
	deps := service.TenantPurgerDeps{
		Retention: r, Data: d, Batch: 50, Interval: time.Hour,
		Now:    func() time.Time { return w5cPurgeNow },
		Record: func(table string, n int64) { rec[table] += n }, Failed: func() {},
	}
	purger := service.NewTenantPurger(deps)

	err := purger.Tick(context.Background())

	require.ErrorContains(t, err, "mark tenant "+tenant.String()+" purged")
	require.ErrorIs(t, err, r.markErr)
	assert.Empty(t, r.purged)
	assert.Equal(t, 2, d.calls[tenant], "one batch that deleted rows and one that found none")
	assert.Equal(t, map[string]int64{"workflow": 3}, rec)
	assert.Equal(t, []recordedFailure{{tenant, 1, 1, w5cPurgeNow.Add(time.Hour)}}, r.failures)

	r.markErr = nil
	require.NoError(t, purger.Tick(context.Background()))

	assert.Equal(t, 3, d.calls[tenant], "the second tick runs one batch, which finds nothing")
	assert.Equal(t, []uuid.UUID{tenant}, r.purged, "the mark is retried and succeeds")
	assert.Equal(t, map[string]int64{"workflow": 3}, rec, "nothing is counted twice")
	assert.Len(t, r.failures, 1)
}

func TestTenantPurger_RecordChangedBeforeASecretDelete_StopsThereAndRecordsNoFailure(t *testing.T) {
	cases := []struct {
		name        string
		failAtCall  int
		wantDeleted int
	}{
		{name: "before the first path", failAtCall: 1, wantDeleted: 0},
		{name: "before the second path", failAtCall: 2, wantDeleted: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant := uuid.New()
			paths := w5cSecretPaths(t, tenant)
			r := &w5cRetention{currentErr: func(call int) error {
				if call == tc.failAtCall {
					return fmt.Errorf("check: %w", domain.ErrTenantRetentionChanged)
				}
				return nil
			}}
			r.due = bothDue(tenant)
			secrets := &w5cSecrets{}
			d := &fakeDataPurger{}
			rig := w5cNewPurger(r, d, secrets, time.Hour)

			require.NoError(t, rig.purger.Tick(context.Background()))

			assert.True(t, slices.Equal(paths[:tc.wantDeleted], secrets.deleted), "only the paths before the changed check are deleted, got %v", secrets.deleted)
			require.Len(t, r.currentCalls, tc.failAtCall)
			for _, c := range r.currentCalls {
				assert.Equal(t, w5cCurrentCall{tenant, 1}, c)
			}
			assert.Empty(t, r.credsDone)
			assert.Empty(t, r.failures)
			assert.Zero(t, *rig.failed)
			assert.Empty(t, d.calls, "the tenant is skipped, so the artifacts step does not run")
			assert.Empty(t, r.purged)
			assert.Equal(t, []string{"tenant purge skipped: its record changed"}, rig.log.infos)
		})
	}
}

func TestTenantPurger_CurrentCheckFailsForAnotherReason_DeletesNothingAndRecordsTheFailure(t *testing.T) {
	tenant := uuid.New()
	down := errors.New("ledger read failed")
	r := &w5cRetention{currentErr: func(int) error { return down }}
	r.due = bothDue(tenant)
	secrets := &w5cSecrets{}
	rig := w5cNewPurger(r, &fakeDataPurger{}, secrets, time.Hour)

	err := rig.purger.Tick(context.Background())

	require.ErrorIs(t, err, down)
	assert.Empty(t, secrets.deleted)
	assert.Len(t, r.currentCalls, 1, "the credentials step stops at the first failed check")
	assert.Empty(t, r.credsDone)
	assert.Equal(t, 1, *rig.failed)
	assert.Equal(t, []recordedFailure{{tenant, 2, 1, w5cPurgeNow.Add(time.Hour)}}, r.failures, "the failure is recorded against the version the artifacts step wrote")
	assert.Equal(t, []uuid.UUID{tenant}, r.purged, "the artifacts step still runs")
}

func TestTenantPurger_SecretDeleteFailsOnOnePath_StopsThereKeepsEarlierDeletesAndNamesThePath(t *testing.T) {
	tenant := uuid.New()
	paths := w5cSecretPaths(t, tenant)
	cases := []struct {
		name  string
		index int
	}{
		{name: "the first path", index: 0},
		{name: "a middle path", index: 1},
		{name: "the last path", index: len(paths) - 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cause := errors.New("openbao down")
			r := &w5cRetention{}
			r.due = []port.TenantPurgeDue{{TenantID: tenant, Credentials: true, RecordVersion: 1}}
			secrets := &w5cSecrets{failOn: paths[tc.index], cause: cause}
			rig := w5cNewPurger(r, &fakeDataPurger{}, secrets, time.Hour)

			err := rig.purger.Tick(context.Background())

			require.ErrorIs(t, err, cause)
			assert.ErrorContains(t, err, "delete credential "+paths[tc.index])
			assert.Equal(t, paths[:tc.index+1], secrets.deleted, "paths after the failing one are not attempted, earlier ones stay deleted")
			assert.Empty(t, r.credsDone)
			assert.Equal(t, 1, *rig.failed)
			assert.Equal(t, []recordedFailure{{tenant, 1, 1, w5cPurgeNow.Add(time.Hour)}}, r.failures)
		})
	}
}

func TestTenantPurger_Run_KeepsSweepingAfterAFailedSweepAndReturnsOnceCancelled(t *testing.T) {
	tenant := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &w5cRetention{}
	r.due = []port.TenantPurgeDue{{TenantID: tenant, Artifacts: true, RecordVersion: 1}}
	sweeps := 0
	r.onList = func() {
		sweeps++
		switch sweeps {
		case 1:
			r.listErr = errors.New("db down")
		case 2:
			r.listErr = nil
		default:
			cancel()
		}
	}
	rig := w5cNewPurger(r, &fakeDataPurger{}, nil, time.Millisecond)

	done := make(chan struct{})
	go func() { rig.purger.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}

	assert.GreaterOrEqual(t, sweeps, 3, "the loop sweeps again on the ticker after the first sweep failed")
	require.NotEmpty(t, rig.log.errors)
	assert.Equal(t, "tenant purge sweep failed", rig.log.errors[0], "the failed sweep is logged and does not stop the loop")
	assert.NotEmpty(t, r.purged, "the sweep after the failed one purged the due tenant")
	assert.Equal(t, tenant, r.purged[0])
}
