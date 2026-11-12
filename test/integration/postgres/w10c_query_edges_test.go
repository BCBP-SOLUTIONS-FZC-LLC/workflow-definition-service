//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

type w10cRetentionRow struct {
	OffboardedAt          *time.Time
	CredentialsPurgeAfter *time.Time
	ArtifactsPurgeAfter   *time.Time
	CredentialsPurgedAt   *time.Time
	PurgedAt              *time.Time
	ReturnedAt            *time.Time
	Attempts              int
	NextAttemptAt         time.Time
	RecordVersion         int64
}

func w10cAt(hours int) time.Time {
	return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(hours) * time.Hour)
}

func w10cPtr(tm time.Time) *time.Time { return &tm }

func w10cUTC(tm *time.Time) *time.Time {
	if tm == nil {
		return nil
	}
	utc := tm.UTC()
	return &utc
}

func w10cReadRetention(t *testing.T, pool *pgcommon.Pool, tenant uuid.UUID) w10cRetentionRow {
	t.Helper()
	var row w10cRetentionRow
	require.NoError(t, pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx,
			`SELECT offboarded_at, credentials_purge_after, artifacts_purge_after, credentials_purged_at,
			        purged_at, returned_at, attempts, next_attempt_at, record_version
			   FROM tenant_retention WHERE tenant_id = $1`, tenant).
			Scan(&row.OffboardedAt, &row.CredentialsPurgeAfter, &row.ArtifactsPurgeAfter, &row.CredentialsPurgedAt,
				&row.PurgedAt, &row.ReturnedAt, &row.Attempts, &row.NextAttemptAt, &row.RecordVersion)
	}))
	for _, field := range []**time.Time{&row.OffboardedAt, &row.CredentialsPurgeAfter, &row.ArtifactsPurgeAfter,
		&row.CredentialsPurgedAt, &row.PurgedAt, &row.ReturnedAt} {
		*field = w10cUTC(*field)
	}
	row.NextAttemptAt = row.NextAttemptAt.UTC()
	return row
}

func w10cDueFor(t *testing.T, repo *pgadapter.TenantRetentionRepo, now time.Time, tenants ...uuid.UUID) []port.TenantPurgeDue {
	t.Helper()
	due, err := repo.ListDue(context.Background(), now, 1000)
	require.NoError(t, err)
	mine := map[uuid.UUID]bool{}
	for _, tenant := range tenants {
		mine[tenant] = true
	}
	var out []port.TenantPurgeDue
	for _, d := range due {
		if mine[d.TenantID] {
			out = append(out, d)
		}
	}
	return out
}

func w10cExec(t *testing.T, pool *pgcommon.Pool, query string, args ...any) error {
	t.Helper()
	return pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx, query, args...)
		return err
	})
}

func w10cWorkflow(t *testing.T, pool *pgcommon.Pool, tenant uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, w10cExec(t, pool,
		`INSERT INTO workflow (id, tenant_id, created_by_user_id, template_key, name) VALUES ($1, $2, $3, $4, $5)`,
		id, tenant, uuid.New(), "w10c-"+id.String(), name))
	return id
}

func w10cVersion(t *testing.T, pool *pgcommon.Pool, tenant, workflow uuid.UUID, status string, number *int32, refs string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, w10cExec(t, pool,
		`INSERT INTO workflow_version (id, workflow_id, tenant_id, status, bpmn_xml, version_number, created_by_user_id, module_refs)
		 VALUES ($1, $2, $3, $4::text::workflow_version_status, '<x/>', $5, $6, $7::jsonb)`,
		id, workflow, tenant, status, number, uuid.New(), refs))
	return id
}

func w10cNumber(n int32) *int32 { return &n }

func TestTenantRetentionRepo_OffboardingAfterAReturnStartsAFreshCycle(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	repo := pgadapter.NewTenantRetentionRepo(pool)
	ctx := context.Background()

	t.Run("every purge mark and the attempts are cleared and the new dates are stored", func(t *testing.T) {
		tenant := uuid.New()
		require.NoError(t, repo.RecordOffboarded(ctx, tenant, w10cAt(0), w10cAt(1), w10cAt(2)))
		version, err := repo.MarkCredentialsPurged(ctx, tenant, 1, w10cAt(3))
		require.NoError(t, err)
		version, err = repo.MarkPurged(ctx, tenant, version, w10cAt(4))
		require.NoError(t, err)
		require.NoError(t, repo.RecordFailure(ctx, tenant, version, 4, w10cAt(50)))
		require.NoError(t, repo.RecordReturned(ctx, tenant, w10cAt(10)))
		before := w10cReadRetention(t, pool, tenant)
		require.NotNil(t, before.CredentialsPurgedAt, "precondition: the credentials were purged")
		require.NotNil(t, before.PurgedAt, "precondition: the artifacts were purged")
		require.NotNil(t, before.ReturnedAt, "precondition: the tenant returned")
		require.Equal(t, 4, before.Attempts, "precondition: failed attempts were recorded")

		require.NoError(t, repo.RecordOffboarded(ctx, tenant, w10cAt(20), w10cAt(21), w10cAt(22)))

		assert.Equal(t, w10cRetentionRow{
			OffboardedAt:          w10cPtr(w10cAt(20)),
			CredentialsPurgeAfter: w10cPtr(w10cAt(21)),
			ArtifactsPurgeAfter:   w10cPtr(w10cAt(22)),
			NextAttemptAt:         w10cAt(20),
			RecordVersion:         before.RecordVersion + 1,
		}, w10cReadRetention(t, pool, tenant))
		assert.Equal(t,
			[]port.TenantPurgeDue{{TenantID: tenant, Credentials: true, Artifacts: true, RecordVersion: before.RecordVersion + 1}},
			w10cDueFor(t, repo, w10cAt(100), tenant), "the tenant is due again for both parts with no failed attempts")
	})

	t.Run("an offboarding at the very moment of the return changes nothing but one microsecond later resets", func(t *testing.T) {
		tenant := uuid.New()
		require.NoError(t, repo.RecordOffboarded(ctx, tenant, w10cAt(0), w10cAt(1), w10cAt(2)))
		require.NoError(t, repo.RecordReturned(ctx, tenant, w10cAt(10)))
		returned := w10cReadRetention(t, pool, tenant)
		require.NotNil(t, returned.ReturnedAt)

		require.NoError(t, repo.RecordOffboarded(ctx, tenant, w10cAt(10), w10cAt(11), w10cAt(12)))
		assert.Equal(t, returned, w10cReadRetention(t, pool, tenant), "offboarded_at equal to returned_at is not later than the return")

		later := w10cAt(10).Add(time.Microsecond)
		require.NoError(t, repo.RecordOffboarded(ctx, tenant, later, w10cAt(30), w10cAt(31)))
		assert.Equal(t, w10cRetentionRow{
			OffboardedAt:          w10cPtr(later),
			CredentialsPurgeAfter: w10cPtr(w10cAt(30)),
			ArtifactsPurgeAfter:   w10cPtr(w10cAt(31)),
			NextAttemptAt:         later,
			RecordVersion:         returned.RecordVersion + 1,
		}, w10cReadRetention(t, pool, tenant))
	})
}

func TestTenantRetentionRepo_AnOlderOrRepeatedEventLeavesTheRowAsItWas(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	repo := pgadapter.NewTenantRetentionRepo(pool)
	ctx := context.Background()

	replays := []struct {
		name                           string
		offboarded, credentials, files time.Time
	}{
		{"with later dates", w10cAt(72), w10cAt(96), w10cAt(120)},
		{"with earlier dates", w10cAt(-72), w10cAt(-48), w10cAt(-24)},
	}
	for _, replay := range replays {
		t.Run("an offboarding replayed "+replay.name+" keeps the purge progress and the failed attempts", func(t *testing.T) {
			tenant := uuid.New()
			require.NoError(t, repo.RecordOffboarded(ctx, tenant, w10cAt(0), w10cAt(1), w10cAt(2)))
			version, err := repo.MarkCredentialsPurged(ctx, tenant, 1, w10cAt(3))
			require.NoError(t, err)
			version, err = repo.MarkPurged(ctx, tenant, version, w10cAt(4))
			require.NoError(t, err)
			require.NoError(t, repo.RecordFailure(ctx, tenant, version, 2, w10cAt(50)))
			before := w10cReadRetention(t, pool, tenant)
			require.NotNil(t, before.CredentialsPurgedAt, "precondition: credentials purged")
			require.NotNil(t, before.PurgedAt, "precondition: artifacts purged")
			require.Equal(t, 2, before.Attempts, "precondition: attempts recorded")
			require.Equal(t, w10cAt(50), before.NextAttemptAt, "precondition: next attempt recorded")
			require.Nil(t, before.ReturnedAt, "precondition: never returned")

			require.NoError(t, repo.RecordOffboarded(ctx, tenant, replay.offboarded, replay.credentials, replay.files))

			assert.Equal(t, before, w10cReadRetention(t, pool, tenant))
		})
	}

	t.Run("a return older than or equal to the stored return is ignored and a later one replaces it", func(t *testing.T) {
		tenant := uuid.New()
		require.NoError(t, repo.RecordReturned(ctx, tenant, w10cAt(10)))
		stored := w10cReadRetention(t, pool, tenant)
		require.Equal(t, w10cPtr(w10cAt(10)), stored.ReturnedAt)
		require.Nil(t, stored.OffboardedAt, "a return can be recorded before any offboarding")

		require.NoError(t, repo.RecordReturned(ctx, tenant, w10cAt(5)))
		assert.Equal(t, stored, w10cReadRetention(t, pool, tenant), "an older return")
		require.NoError(t, repo.RecordReturned(ctx, tenant, w10cAt(10)))
		assert.Equal(t, stored, w10cReadRetention(t, pool, tenant), "a redelivered return")

		require.NoError(t, repo.RecordReturned(ctx, tenant, w10cAt(11)))
		got := w10cReadRetention(t, pool, tenant)
		assert.Equal(t, w10cPtr(w10cAt(11)), got.ReturnedAt)
		assert.Equal(t, stored.RecordVersion+1, got.RecordVersion)
	})

	t.Run("a return that is not later than the offboarding is ignored", func(t *testing.T) {
		tenant := uuid.New()
		require.NoError(t, repo.RecordOffboarded(ctx, tenant, w10cAt(5), w10cAt(6), w10cAt(7)))
		stored := w10cReadRetention(t, pool, tenant)

		require.NoError(t, repo.RecordReturned(ctx, tenant, w10cAt(4)))
		assert.Equal(t, stored, w10cReadRetention(t, pool, tenant), "a return before the offboarding")
		require.NoError(t, repo.RecordReturned(ctx, tenant, w10cAt(5)))
		assert.Equal(t, stored, w10cReadRetention(t, pool, tenant), "a return at the offboarding moment")

		require.NoError(t, repo.RecordReturned(ctx, tenant, w10cAt(5).Add(time.Microsecond)))
		got := w10cReadRetention(t, pool, tenant)
		assert.Equal(t, w10cPtr(w10cAt(5).Add(time.Microsecond)), got.ReturnedAt)
		assert.Equal(t, stored.RecordVersion+1, got.RecordVersion)
	})

	t.Run("an offboarding older than the stored return of a tenant never offboarded is ignored", func(t *testing.T) {
		tenant := uuid.New()
		require.NoError(t, repo.RecordReturned(ctx, tenant, w10cAt(10)))
		stored := w10cReadRetention(t, pool, tenant)

		require.NoError(t, repo.RecordOffboarded(ctx, tenant, w10cAt(3), w10cAt(4), w10cAt(5)))

		assert.Equal(t, stored, w10cReadRetention(t, pool, tenant))
		assert.Empty(t, w10cDueFor(t, repo, w10cAt(100), tenant))
	})
}

func TestTenantRetentionRepo_ListDue_OrdersByEarliestPurgeDateThenTenantAndIncludesTheNowBoundary(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	repo := pgadapter.NewTenantRetentionRepo(pool)
	ctx := context.Background()

	t.Run("the earlier of the two purge dates decides the order and the tenant id breaks ties", func(t *testing.T) {
		early, tieA, tieB, late := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		dates := []struct {
			tenant      uuid.UUID
			credentials int
			artifacts   int
		}{
			{late, 4, 8},
			{tieB, 3, 7},
			{early, 9, 2},
			{tieA, 3, 3},
		}
		for _, d := range dates {
			require.NoError(t, repo.RecordOffboarded(ctx, d.tenant, w10cAt(0), w10cAt(d.credentials), w10cAt(d.artifacts)))
		}
		tied := []uuid.UUID{tieA, tieB}
		sort.Slice(tied, func(i, j int) bool { return bytes.Compare(tied[i][:], tied[j][:]) < 0 })

		due := w10cDueFor(t, repo, w10cAt(100), early, tieA, tieB, late)

		var order []uuid.UUID
		for _, d := range due {
			order = append(order, d.TenantID)
			assert.True(t, d.Credentials && d.Artifacts, "both parts are due for %s", d.TenantID)
		}
		assert.Equal(t, []uuid.UUID{early, tied[0], tied[1], late}, order)
	})

	t.Run("a purge date or next attempt equal to now is due and one microsecond later is not", func(t *testing.T) {
		now := w10cAt(100)
		after := now.Add(time.Microsecond)
		bothAtNow, mixed, notYet, waiting, retryAtNow, onePartPurged := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
		require.NoError(t, repo.RecordOffboarded(ctx, bothAtNow, w10cAt(0), now, now))
		require.NoError(t, repo.RecordOffboarded(ctx, mixed, w10cAt(0), now, after))
		require.NoError(t, repo.RecordOffboarded(ctx, notYet, w10cAt(0), after, after))
		require.NoError(t, repo.RecordOffboarded(ctx, waiting, w10cAt(0), w10cAt(1), w10cAt(2)))
		require.NoError(t, repo.RecordFailure(ctx, waiting, 1, 1, after))
		require.NoError(t, repo.RecordOffboarded(ctx, retryAtNow, w10cAt(0), w10cAt(1), w10cAt(2)))
		require.NoError(t, repo.RecordFailure(ctx, retryAtNow, 1, 1, now))
		require.NoError(t, repo.RecordOffboarded(ctx, onePartPurged, w10cAt(0), w10cAt(1), after))
		_, err := repo.MarkCredentialsPurged(ctx, onePartPurged, 1, w10cAt(3))
		require.NoError(t, err)

		due := w10cDueFor(t, repo, now, bothAtNow, mixed, notYet, waiting, retryAtNow, onePartPurged)

		got := map[uuid.UUID]port.TenantPurgeDue{}
		for _, d := range due {
			got[d.TenantID] = d
		}
		assert.Equal(t, map[uuid.UUID]port.TenantPurgeDue{
			bothAtNow:  {TenantID: bothAtNow, Credentials: true, Artifacts: true, RecordVersion: 1},
			mixed:      {TenantID: mixed, Credentials: true, Artifacts: false, RecordVersion: 1},
			retryAtNow: {TenantID: retryAtNow, Credentials: true, Artifacts: true, Attempts: 1, RecordVersion: 2},
		}, got)
	})
}

func TestTenantRetentionRepo_HasRows_IsTrueForEachCountedTableAndIgnoresTheGlobalCatalog(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	repo := pgadapter.NewTenantRetentionRepo(fixtures.NewContextGUCRolePool(t, dsn))
	ctx := context.Background()
	globalModule := uuid.New()
	require.NoError(t, w10cExec(t, superPool,
		`INSERT INTO workflow_module (id, scope, name, process_id) VALUES ($1, 'global', 'g', $2)`, globalModule, globalModule.String()))
	require.NoError(t, w10cExec(t, superPool,
		`INSERT INTO workflow_template (id, scope, name, bpmn_xml) VALUES ($1, 'global', 'g', '<x/>')`, uuid.New()))
	bystander := uuid.New()
	w10cWorkflow(t, superPool, bystander, "bystander")

	cases := []struct {
		name string
		seed func(t *testing.T, tenant uuid.UUID)
		want bool
	}{
		{"a workflow alone", func(t *testing.T, tenant uuid.UUID) {
			w10cWorkflow(t, superPool, tenant, "alone")
		}, true},
		{"a tenant-scope module alone", func(t *testing.T, tenant uuid.UUID) {
			id := uuid.New()
			require.NoError(t, w10cExec(t, superPool,
				`INSERT INTO workflow_module (id, tenant_id, scope, name, process_id) VALUES ($1, $2, 'tenant', 'm', $3)`, id, tenant, id.String()))
		}, true},
		{"a tenant-scope starter alone", func(t *testing.T, tenant uuid.UUID) {
			require.NoError(t, w10cExec(t, superPool,
				`INSERT INTO workflow_template (id, tenant_id, scope, name, bpmn_xml) VALUES ($1, $2, 'tenant', 's', '<x/>')`, uuid.New(), tenant))
		}, true},
		{"only the global catalog and another tenant's workflow exist", func(*testing.T, uuid.UUID) {}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant := uuid.New()
			tc.seed(t, tenant)

			has, err := repo.HasRows(ctx, tenant)

			require.NoError(t, err)
			assert.Equal(t, tc.want, has)
		})
	}

	t.Run("a global module or starter cannot be stored with a tenant id", func(t *testing.T) {
		tenant := uuid.New()
		id := uuid.New()
		require.Error(t, w10cExec(t, superPool,
			`INSERT INTO workflow_module (id, tenant_id, scope, name, process_id) VALUES ($1, $2, 'global', 'm', $3)`, id, tenant, id.String()))
		require.Error(t, w10cExec(t, superPool,
			`INSERT INTO workflow_template (id, tenant_id, scope, name, bpmn_xml) VALUES ($1, $2, 'global', 's', '<x/>')`, uuid.New(), tenant))

		has, err := repo.HasRows(ctx, tenant)

		require.NoError(t, err)
		assert.False(t, has)
	})
}

type w10cEntry struct {
	NodeID string `json:"node_id"`
	Error  string `json:"error"`
}

func w10cStoredEntries(t *testing.T, repo *pgadapter.WorkflowVersionRepo, tenant, version uuid.UUID) []w10cEntry {
	t.Helper()
	got, err := repo.GetByID(context.Background(), tenant, version)
	require.NoError(t, err)
	require.NotNil(t, got.ValidationErrorsJSON)
	var entries []w10cEntry
	require.NoError(t, json.Unmarshal([]byte(*got.ValidationErrorsJSON), &entries))
	return entries
}

func w10cGone(nodes ...string) []w10cEntry {
	entries := make([]w10cEntry, len(nodes))
	for i, n := range nodes {
		entries[i] = w10cEntry{NodeID: n, Error: "gone"}
	}
	return entries
}

func w10cJSON(t *testing.T, entries []w10cEntry) string {
	t.Helper()
	b, err := json.Marshal(entries)
	require.NoError(t, err)
	return string(b)
}

func TestWorkflowVersionRepo_SetInvalid_KeepsFirstOccurrenceOrderWithoutRepeats(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	repo := pgadapter.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	tenant := uuid.New()
	version := w10cVersion(t, pool, tenant, w10cWorkflow(t, pool, tenant, "wf"), "DRAFT", nil, "[]")

	steps := []struct {
		name  string
		input []w10cEntry
		want  []w10cEntry
	}{
		{"the first call stores its list as given", w10cGone("C", "A"), w10cGone("C", "A")},
		{"new entries follow the stored ones and a stored entry keeps its place", w10cGone("B", "C", "A"), w10cGone("C", "A", "B")},
		{"an entry repeated inside one call is stored once", w10cGone("A", "D", "D"), w10cGone("C", "A", "B", "D")},
		{"an empty list changes nothing", nil, w10cGone("C", "A", "B", "D")},
		{"the same node with another message is a different entry", []w10cEntry{{NodeID: "C", Error: "changed"}},
			append(w10cGone("C", "A", "B", "D"), w10cEntry{NodeID: "C", Error: "changed"})},
		{"a replay of an earlier call changes nothing", w10cGone("B", "C", "A"),
			append(w10cGone("C", "A", "B", "D"), w10cEntry{NodeID: "C", Error: "changed"})},
	}
	for _, step := range steps {
		input := "[]"
		if step.input != nil {
			input = w10cJSON(t, step.input)
		}

		require.NoError(t, repo.SetInvalid(ctx, tenant, version, input), step.name)

		assert.Equal(t, step.want, w10cStoredEntries(t, repo, tenant, version), step.name)
	}
	got, err := repo.GetByID(ctx, tenant, version)
	require.NoError(t, err)
	assert.False(t, got.IsValid)
}

func TestWorkflowVersionRepo_SetInvalid_ANonArrayStoredOrGivenValueCountsAsEmpty(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	repo := pgadapter.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	stored := `[{"node_id":"X","error":"gone"}]`

	cases := []struct {
		name   string
		stored *string
		input  string
		want   []w10cEntry
	}{
		{"nothing stored and an array given", nil, w10cJSON(t, w10cGone("A")), w10cGone("A")},
		{"nothing stored and an object given", nil, `{"node_id":"A","error":"gone"}`, []w10cEntry{}},
		{"a stored object is replaced by the array given", new(`{"node_id":"X","error":"gone"}`), w10cJSON(t, w10cGone("A")), w10cGone("A")},
		{"a stored string is replaced by the array given", new(`"text"`), w10cJSON(t, w10cGone("A")), w10cGone("A")},
		{"a stored JSON null is replaced by the array given", new(`null`), w10cJSON(t, w10cGone("A")), w10cGone("A")},
		{"an object given keeps the stored array", &stored, `{"node_id":"A","error":"gone"}`, w10cGone("X")},
		{"a string given keeps the stored array", &stored, `"text"`, w10cGone("X")},
		{"a JSON null given keeps the stored array", &stored, `null`, w10cGone("X")},
		{"a number given keeps the stored array", &stored, `7`, w10cGone("X")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant := uuid.New()
			version := w10cVersion(t, pool, tenant, w10cWorkflow(t, pool, tenant, "wf"), "DRAFT", nil, "[]")
			require.NoError(t, w10cExec(t, pool,
				`UPDATE workflow_version SET validation_errors_json = $1::jsonb WHERE id = $2`, tc.stored, version))

			require.NoError(t, repo.SetInvalid(ctx, tenant, version, tc.input))

			got := w10cStoredEntries(t, repo, tenant, version)
			assert.Equal(t, tc.want, got)
			read, err := repo.GetByID(ctx, tenant, version)
			require.NoError(t, err)
			assert.False(t, read.IsValid)
		})
	}
}

func TestWorkflowVersionRepo_SetInvalid_RevocationsBuiltFromTheAssigneeListMergeInOrderAndReplaySafely(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	versions := pgadapter.NewWorkflowVersionRepo(pool)
	assignees := pgadapter.NewAssigneeRepo(pool)
	ctx := context.Background()
	tenant, user, colleague := uuid.New(), uuid.New(), uuid.New()
	workflow := w10cWorkflow(t, pool, tenant, "wf")
	published := w10cVersion(t, pool, tenant, workflow, "PUBLISHED", w10cNumber(1), "[]")
	draft := w10cVersion(t, pool, tenant, workflow, "DRAFT", nil, "[]")
	assign := func(version uuid.UUID, who uuid.UUID, nodes ...string) {
		t.Helper()
		var list []*domain.NodeAssignee
		for _, n := range nodes {
			list = append(list, &domain.NodeAssignee{ID: uuid.New(), NodeKey: n, UserID: who, DepartmentID: uuid.New(), Role: "reviewer"})
		}
		require.NoError(t, assignees.BulkInsert(ctx, tenant, version, list))
	}
	assign(draft, user, "Task_b", "Task_a")
	assign(published, user, "Task_c")
	assign(published, colleague, "Task_z")

	revoke := func() map[uuid.UUID][]string {
		t.Helper()
		listed, err := assignees.ListByUser(ctx, tenant, user)
		require.NoError(t, err)
		byVersion := map[uuid.UUID][]string{}
		for _, a := range listed {
			byVersion[a.WorkflowVersionID] = append(byVersion[a.WorkflowVersionID], a.NodeKey)
		}
		for version, nodes := range byVersion {
			require.NoError(t, versions.SetInvalid(ctx, tenant, version, w10cJSON(t, w10cGone(nodes...))))
		}
		return byVersion
	}

	listed, err := assignees.ListByUser(ctx, tenant, user)
	require.NoError(t, err)
	type pair struct {
		version uuid.UUID
		node    string
	}
	var gotOrder, wantOrder []pair
	for _, a := range listed {
		gotOrder = append(gotOrder, pair{a.WorkflowVersionID, a.NodeKey})
	}
	wantOrder = []pair{{published, "Task_c"}, {draft, "Task_a"}, {draft, "Task_b"}}
	sort.SliceStable(wantOrder, func(i, j int) bool {
		if c := bytes.Compare(wantOrder[i].version[:], wantOrder[j].version[:]); c != 0 {
			return c < 0
		}
		return wantOrder[i].node < wantOrder[j].node
	})
	assert.Equal(t, wantOrder, gotOrder, "assignments come back ordered by version id, then node key")
	other, err := assignees.ListByUser(ctx, uuid.New(), user)
	require.NoError(t, err)
	assert.Empty(t, other, "another tenant sees none of the user's assignments")

	revoke()
	revoke()
	assert.Equal(t, w10cGone("Task_a", "Task_b"), w10cStoredEntries(t, versions, tenant, draft), "two deliveries of one revocation")
	assert.Equal(t, w10cGone("Task_c"), w10cStoredEntries(t, versions, tenant, published), "the colleague's node is not listed")

	assign(draft, user, "Task_0")
	revoke()
	revoke()
	assert.Equal(t, w10cGone("Task_a", "Task_b", "Task_0"), w10cStoredEntries(t, versions, tenant, draft),
		"the second revocation adds its node after the stored ones and a replay adds nothing")
	assert.Equal(t, w10cGone("Task_c"), w10cStoredEntries(t, versions, tenant, published))
}

func TestWorkflowVersionRepo_NextVersionNumber_IsOneMoreThanTheHighestNonDraftNumber(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	repo := pgadapter.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	type seeded struct {
		status string
		number *int32
	}

	cases := []struct {
		name     string
		versions []seeded
		want     int32
	}{
		{"no versions", nil, 1},
		{"only a draft", []seeded{{"DRAFT", nil}}, 1},
		{"one published version", []seeded{{"PUBLISHED", w10cNumber(1)}}, 2},
		{"only an archived version", []seeded{{"ARCHIVED", w10cNumber(4)}}, 5},
		{"published and archived with a gap between them", []seeded{{"PUBLISHED", w10cNumber(1)}, {"ARCHIVED", w10cNumber(3)}}, 4},
		{"published, archived and a draft", []seeded{{"ARCHIVED", w10cNumber(1)}, {"PUBLISHED", w10cNumber(2)}, {"DRAFT", nil}}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant := uuid.New()
			workflow := w10cWorkflow(t, pool, tenant, "wf")
			sibling := w10cWorkflow(t, pool, tenant, "sibling")
			w10cVersion(t, pool, tenant, sibling, "PUBLISHED", w10cNumber(9), "[]")
			for _, v := range tc.versions {
				w10cVersion(t, pool, tenant, workflow, v.status, v.number, "[]")
			}

			got, err := repo.NextVersionNumber(ctx, tenant, workflow)

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			foreign, err := repo.NextVersionNumber(ctx, uuid.New(), workflow)
			require.NoError(t, err)
			assert.Equal(t, int32(1), foreign, "another tenant's count starts again")
		})
	}
}

func TestWorkflowVersionRepo_ListModuleUsages_OrdersByWorkflowNameThenNewestVersionWithDraftsLast(t *testing.T) {
	pool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	tenant, user := uuid.New(), uuid.New()
	repo := pgadapter.NewWorkflowVersionRepo(fixtures.NewRestrictedRolePool(t, dsn, false, &tenant, &user))
	module, otherModule := uuid.New(), uuid.New()
	refs := func(entries ...any) string {
		t.Helper()
		var list []map[string]any
		for i := 0; i < len(entries); i += 2 {
			list = append(list, map[string]any{
				"module_id": entries[i].(uuid.UUID).String(), "version_id": uuid.NewString(),
				"process_id": "Process_W10c", "version_number": entries[i+1],
			})
		}
		b, err := json.Marshal(list)
		require.NoError(t, err)
		return string(b)
	}
	beta := w10cWorkflow(t, pool, tenant, "beta")
	alpha := w10cWorkflow(t, pool, tenant, "alpha")
	gamma := w10cWorkflow(t, pool, tenant, "gamma")
	alphaArchived := w10cVersion(t, pool, tenant, alpha, "ARCHIVED", w10cNumber(1), refs(module, 2, otherModule, 1, module, 1))
	alphaPublished := w10cVersion(t, pool, tenant, alpha, "PUBLISHED", w10cNumber(2), refs(module, 1))
	alphaDraft := w10cVersion(t, pool, tenant, alpha, "DRAFT", nil, refs(module, 3))
	w10cVersion(t, pool, tenant, alpha, "PUBLISHED", w10cNumber(3), "[]")
	betaPublished := w10cVersion(t, pool, tenant, beta, "PUBLISHED", w10cNumber(3), refs(module, 1, otherModule, 1, module, 2))
	w10cVersion(t, pool, tenant, gamma, "PUBLISHED", w10cNumber(1), refs(otherModule, 1))

	got, err := repo.ListModuleUsages(ctx, tenant, module)

	require.NoError(t, err)
	type usage struct {
		workflow      string
		version       uuid.UUID
		number        int32
		status        domain.VersionStatus
		moduleVersion int32
	}
	var gotUsages []usage
	for _, u := range got {
		number := int32(-1)
		if u.VersionNumber != nil {
			number = *u.VersionNumber
		}
		gotUsages = append(gotUsages, usage{u.WorkflowName, u.VersionID, number, u.Status, u.ModuleVersionNumber})
	}
	assert.Equal(t, []usage{
		{"alpha", alphaPublished, 2, domain.VersionStatusPublished, 1},
		{"alpha", alphaArchived, 1, domain.VersionStatusArchived, 1},
		{"alpha", alphaArchived, 1, domain.VersionStatusArchived, 2},
		{"alpha", alphaDraft, -1, domain.VersionStatusDraft, 3},
		{"beta", betaPublished, 3, domain.VersionStatusPublished, 1},
		{"beta", betaPublished, 3, domain.VersionStatusPublished, 2},
	}, gotUsages, "one entry per matching reference, the other module's references and unrelated versions left out")
}

func TestRLSViolationRepo_PruneBefore_KeepsTheRowAtTheCutoffAndCountsOnlyOlderOnes(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	repo := pgadapter.NewRLSViolationRepo(pool)
	ctx := context.Background()
	cutoff := w10cAt(0)

	empty, err := repo.PruneBefore(ctx, cutoff)
	require.NoError(t, err)
	assert.Zero(t, empty, "nothing to delete")

	for _, at := range []time.Time{cutoff.Add(-time.Microsecond), cutoff, cutoff.Add(time.Microsecond)} {
		require.NoError(t, w10cExec(t, pool,
			`INSERT INTO rls_violation_log (violation_type, row_tenant_id, table_name, occurred_at) VALUES ('cross_tenant_access', $1, 'workflow', $2)`,
			uuid.New(), at))
	}

	pruned, err := repo.PruneBefore(ctx, cutoff)

	require.NoError(t, err)
	assert.EqualValues(t, 1, pruned)
	var remaining []time.Time
	require.NoError(t, pool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		rows, err := conn.Query(ctx, `SELECT occurred_at FROM rls_violation_log ORDER BY occurred_at`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var at time.Time
			if err := rows.Scan(&at); err != nil {
				return err
			}
			remaining = append(remaining, at.UTC())
		}
		return rows.Err()
	}))
	assert.Equal(t, []time.Time{cutoff, cutoff.Add(time.Microsecond)}, remaining, "the row exactly at the cutoff stays")
	again, err := repo.PruneBefore(ctx, cutoff)
	require.NoError(t, err)
	assert.Zero(t, again, "a second run deletes nothing")
}

func TestWorkflowVersionRepo_GetByID_AnotherTenantsVersionAnswersLikeAMissingOne(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	owner, caller, user := uuid.New(), uuid.New(), uuid.New()
	version := w10cVersion(t, superPool, owner, w10cWorkflow(t, superPool, owner, "wf"), "DRAFT", nil, "[]")
	callerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &caller, &user)
	ownerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &owner, &user)

	cases := []struct {
		name     string
		pool     *pgcommon.Pool
		tenant   uuid.UUID
		version  uuid.UUID
		wantFind bool
	}{
		{"the owner on the owner's pool finds it", ownerPool, owner, version, true},
		{"the caller's tenant id on the plain pool", superPool, caller, version, false},
		{"the owner's id asked on the caller's restricted pool", callerPool, owner, version, false},
		{"the caller's own id asked on the caller's restricted pool", callerPool, caller, version, false},
		{"a version id that does not exist", superPool, owner, uuid.New(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pgadapter.NewWorkflowVersionRepo(tc.pool).GetByID(ctx, tc.tenant, tc.version)

			if tc.wantFind {
				require.NoError(t, err)
				assert.Equal(t, version, got.ID)
				return
			}
			require.ErrorIs(t, err, domain.ErrNotFound, fmt.Sprint(err))
			assert.Nil(t, got)
		})
	}
}
