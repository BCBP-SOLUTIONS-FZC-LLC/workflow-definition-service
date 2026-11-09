//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func versionListWorkflow(t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID) *domain.Workflow {
	t.Helper()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: "wv-list-" + uuid.NewString(), Name: "X",
	}
	if err := postgres.NewWorkflowRepo(pool).Create(context.Background(), wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	return wf
}

func versionListInsertArchived(
	t *testing.T, pool *pgcommon.Pool, wf *domain.Workflow, count int,
) map[int32]uuid.UUID {
	t.Helper()
	ids := make(map[int32]uuid.UUID, count)
	repo := postgres.NewWorkflowVersionRepo(pool)
	for n := int32(1); n <= int32(count); n++ {
		number := n
		v := newDraftVersion(wf.ID, wf.TenantID)
		v.Status = domain.VersionStatusArchived
		v.VersionNumber = &number
		if err := repo.Create(context.Background(), v); err != nil {
			t.Fatalf("create version %d: %v", n, err)
		}
		ids[n] = v.ID
	}
	return ids
}

func versionListSetCreatedAt(t *testing.T, pool *pgcommon.Pool, id uuid.UUID, at time.Time) {
	t.Helper()
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx, `UPDATE workflow_version SET created_at = $2 WHERE id = $1`, id, at)
		return err
	})
	if err != nil {
		t.Fatalf("set created_at: %v", err)
	}
}

func versionListNumbers(versions []*domain.WorkflowVersion) []int32 {
	numbers := make([]int32, 0, len(versions))
	for _, v := range versions {
		if v.VersionNumber == nil {
			numbers = append(numbers, 0)
			continue
		}
		numbers = append(numbers, *v.VersionNumber)
	}
	return numbers
}

func versionListEqual(got, want []int32) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func versionListRolePool(
	t *testing.T, dsn string, tenantID uuid.UUID, grants func(role string) []string,
) *pgcommon.Pool {
	t.Helper()
	ctx := context.Background()
	role := "vlist_" + strings.ReplaceAll(uuid.NewString(), "-", "")

	admin, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer admin.Close()
	statements := append([]string{
		fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD 'restricted' NOBYPASSRLS`, role),
	}, grants(role)...)
	err = admin.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		for _, stmt := range statements {
			if _, err := conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("%s: %w", stmt, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("prepare role: %v", err)
	}
	t.Cleanup(func() {
		cleanup, err := pgcommon.NewPool(context.Background(), pgcommon.Config{DSN: dsn})
		if err != nil {
			return
		}
		defer cleanup.Close()
		_ = cleanup.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
			_, _ = conn.Exec(ctx, fmt.Sprintf(`DROP OWNED BY %s`, role))
			_, _ = conn.Exec(ctx, fmt.Sprintf(`DROP ROLE IF EXISTS %s`, role))
			return nil
		})
	})

	before, after, _ := strings.Cut(dsn, "@")
	scheme, _, _ := strings.Cut(before, "://")
	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{
		DSN: fmt.Sprintf("%s://%s:restricted@%s", scheme, role, after),
		GUCProvider: func(context.Context) (pgdomain.GUCSet, bool) {
			return pgdomain.GUCSet{TenantID: tenantID.String(), UserID: uuid.NewString()}, true
		},
	})
	if err != nil {
		t.Fatalf("open role pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestWorkflowVersionRepo_ListByWorkflow_SeveralVersions_NewestCreatedFirst(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tenantID := uuid.New()
	wf := versionListWorkflow(t, pool, tenantID)

	ids := versionListInsertArchived(t, pool, wf, 3)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	versionListSetCreatedAt(t, pool, ids[1], base.Add(3*time.Hour))
	versionListSetCreatedAt(t, pool, ids[2], base.Add(1*time.Hour))
	versionListSetCreatedAt(t, pool, ids[3], base.Add(2*time.Hour))

	got, total, err := postgres.NewWorkflowVersionRepo(pool).ListByWorkflow(ctx, tenantID, wf.ID, 1, 20)
	if err != nil {
		t.Fatalf("ListByWorkflow: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if want := []int32{1, 3, 2}; !versionListEqual(versionListNumbers(got), want) {
		t.Errorf("version numbers = %v, want %v (newest created_at first, not insertion or number order)",
			versionListNumbers(got), want)
	}
	for i, wantID := range []uuid.UUID{ids[1], ids[3], ids[2]} {
		if got[i].ID != wantID {
			t.Errorf("row %d id = %s, want %s", i, got[i].ID, wantID)
		}
	}
}

func TestWorkflowVersionRepo_ListByWorkflow_Paging(t *testing.T) {
	cases := []struct {
		name      string
		page      int
		limit     int
		wantItems []int32
	}{
		{name: "first page of two", page: 1, limit: 2, wantItems: []int32{3, 2}},
		{name: "last partial page", page: 2, limit: 2, wantItems: []int32{1}},
		{name: "page past the last one", page: 99, limit: 20, wantItems: []int32{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)
			tenantID := uuid.New()
			wf := versionListWorkflow(t, pool, tenantID)
			ids := versionListInsertArchived(t, pool, wf, 3)
			base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			for n, id := range ids {
				versionListSetCreatedAt(t, pool, id, base.Add(time.Duration(n)*time.Hour))
			}

			got, total, err := postgres.NewWorkflowVersionRepo(pool).
				ListByWorkflow(context.Background(), tenantID, wf.ID, tc.page, tc.limit)
			if err != nil {
				t.Fatalf("ListByWorkflow: %v", err)
			}
			if got == nil {
				t.Fatal("versions = nil, want a non-nil slice so the response encodes []")
			}
			if total != 3 {
				t.Errorf("total = %d, want 3 whatever the page", total)
			}
			if !versionListEqual(versionListNumbers(got), tc.wantItems) {
				t.Errorf("version numbers = %v, want %v", versionListNumbers(got), tc.wantItems)
			}
		})
	}
}

func TestWorkflowVersionRepo_ListByWorkflow_OutOfRangePaging_FallsBackToTheFirstPageOfTwenty(t *testing.T) {
	const stored = 21
	newestFirst := func(from, to int32) []int32 {
		out := make([]int32, 0, from-to+1)
		for n := from; n >= to; n-- {
			out = append(out, n)
		}
		return out
	}
	cases := []struct {
		name      string
		page      int
		limit     int
		wantItems []int32
	}{
		{name: "page zero keeps the limit", page: 0, limit: 5, wantItems: newestFirst(21, 17)},
		{name: "negative page keeps the limit", page: -3, limit: 5, wantItems: newestFirst(21, 17)},
		{name: "limit zero becomes twenty", page: 1, limit: 0, wantItems: newestFirst(21, 2)},
		{name: "negative limit keeps the page", page: 2, limit: -1, wantItems: []int32{1}},
		{name: "both out of range", page: 0, limit: 0, wantItems: newestFirst(21, 2)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)
			tenantID := uuid.New()
			wf := versionListWorkflow(t, pool, tenantID)
			ids := versionListInsertArchived(t, pool, wf, stored)
			base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			for n, id := range ids {
				versionListSetCreatedAt(t, pool, id, base.Add(time.Duration(n)*time.Hour))
			}

			got, total, err := postgres.NewWorkflowVersionRepo(pool).
				ListByWorkflow(context.Background(), tenantID, wf.ID, tc.page, tc.limit)
			if err != nil {
				t.Fatalf("ListByWorkflow: %v", err)
			}
			if total != stored {
				t.Errorf("total = %d, want %d", total, stored)
			}
			if !versionListEqual(versionListNumbers(got), tc.wantItems) {
				t.Errorf("version numbers = %v, want %v", versionListNumbers(got), tc.wantItems)
			}
		})
	}
}

func TestWorkflowVersionRepo_ListByWorkflow_OtherTenantsWorkflow_ListsNothing(t *testing.T) {
	cases := []struct {
		name       string
		askAsOwner bool
		pool       func(t *testing.T, superPool *pgcommon.Pool, dsn string, caller uuid.UUID) *pgcommon.Pool
	}{
		{
			name: "query filters on the caller's tenant",
			pool: func(_ *testing.T, superPool *pgcommon.Pool, _ string, _ uuid.UUID) *pgcommon.Pool {
				return superPool
			},
		},
		{
			name:       "row-level security hides the other tenant's rows even when the query names the owner",
			askAsOwner: true,
			pool: func(t *testing.T, _ *pgcommon.Pool, dsn string, caller uuid.UUID) *pgcommon.Pool {
				user := uuid.New()
				return fixtures.NewRestrictedRolePool(t, dsn, false, &caller, &user)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			superPool, dsn := fixtures.NewTestPoolAndDSN(t)
			ctx := context.Background()
			owner, caller := uuid.New(), uuid.New()
			wf := versionListWorkflow(t, superPool, owner)
			versionListInsertArchived(t, superPool, wf, 2)

			callerRepo := postgres.NewWorkflowVersionRepo(tc.pool(t, superPool, dsn, caller))
			queryTenant := caller
			if tc.askAsOwner {
				queryTenant = owner
			}
			got, total, err := callerRepo.ListByWorkflow(ctx, queryTenant, wf.ID, 1, 20)
			if err != nil {
				t.Fatalf("ListByWorkflow: %v", err)
			}
			if len(got) != 0 || total != 0 {
				t.Errorf("caller saw %d versions, total %d; want none", len(got), total)
			}

			ownerGot, ownerTotal, err := postgres.NewWorkflowVersionRepo(superPool).
				ListByWorkflow(ctx, owner, wf.ID, 1, 20)
			if err != nil {
				t.Fatalf("owner list: %v", err)
			}
			if len(ownerGot) != 2 || ownerTotal != 2 {
				t.Errorf("owner saw %d versions, total %d; want 2 each", len(ownerGot), ownerTotal)
			}
		})
	}
}

func TestWorkflowVersionRepo_ListByWorkflow_ClosedPool_ReturnsErrUpstreamUnavailable(t *testing.T) {
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	pool, err := pgcommon.NewPool(context.Background(), pgcommon.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	pool.Close()

	got, total, err := postgres.NewWorkflowVersionRepo(pool).
		ListByWorkflow(context.Background(), uuid.New(), uuid.New(), 1, 20)

	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Fatalf("err = %v, want ErrUpstreamUnavailable", err)
	}
	if got != nil || total != 0 {
		t.Errorf("got %v rows, total %d; want nothing on failure", got, total)
	}
}

func TestWorkflowVersionRepo_ListByWorkflow_QueryFailure_ReturnsUnclassifiedError(t *testing.T) {
	cases := []struct {
		name        string
		grants      func(role string) []string
		countPasses bool
	}{
		{
			name: "count query refused",
			grants: func(string) []string {
				return nil
			},
		},
		{
			name: "list query refused after the count succeeds",
			grants: func(role string) []string {
				return []string{fmt.Sprintf(
					`GRANT SELECT (id, tenant_id, workflow_id) ON workflow_version TO %s`, role)}
			},
			countPasses: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			superPool, dsn := fixtures.NewTestPoolAndDSN(t)
			ctx := context.Background()
			tenantID := uuid.New()
			wf := versionListWorkflow(t, superPool, tenantID)
			versionListInsertArchived(t, superPool, wf, 2)
			restricted := versionListRolePool(t, dsn, tenantID, tc.grants)

			if tc.countPasses {
				var counted int
				err := restricted.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
					return conn.QueryRow(ctx,
						`SELECT COUNT(*) FROM workflow_version WHERE tenant_id = $1 AND workflow_id = $2`,
						tenantID, wf.ID).Scan(&counted)
				})
				if err != nil || counted != 2 {
					t.Fatalf("precondition: count = %d, err = %v; want 2, nil", counted, err)
				}
			}

			got, _, err := postgres.NewWorkflowVersionRepo(restricted).
				ListByWorkflow(ctx, tenantID, wf.ID, 1, 20)

			if err == nil {
				t.Fatal("err = nil, want the database error")
			}
			if errors.Is(err, domain.ErrUpstreamUnavailable) || errors.Is(err, domain.ErrNotFound) {
				t.Errorf("err = %v, want an unclassified error that maps to 500", err)
			}
			if got != nil {
				t.Errorf("got %d rows, want nil on failure", len(got))
			}
		})
	}
}

func TestWorkflowVersionRepo_ListByWorkflow_CountCancelledByTimeout_ReturnsErrUpstreamUnavailable(t *testing.T) {
	cases := []struct {
		name    string
		timeout string
	}{
		{name: "count blocked behind a table lock until the statement timeout", timeout: "300ms"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			superPool, dsn := fixtures.NewTestPoolAndDSN(t)
			ctx := context.Background()
			tenantID := uuid.New()
			wf := versionListWorkflow(t, superPool, tenantID)
			versionListInsertArchived(t, superPool, wf, 1)
			restricted := versionListRolePool(t, dsn, tenantID, func(role string) []string {
				return []string{
					fmt.Sprintf(`GRANT SELECT ON workflow_version TO %s`, role),
					fmt.Sprintf(`ALTER ROLE %s SET statement_timeout = '%s'`, role, tc.timeout),
				}
			})

			var (
				got     []*domain.WorkflowVersion
				total   int64
				listErr error
				repo    = postgres.NewWorkflowVersionRepo(restricted)
			)
			err := superPool.WithTx(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `LOCK TABLE workflow_version IN ACCESS EXCLUSIVE MODE`); err != nil {
					return err
				}
				got, total, listErr = repo.ListByWorkflow(ctx, tenantID, wf.ID, 1, 20)
				return nil
			})
			if err != nil {
				t.Fatalf("hold table lock: %v", err)
			}

			if !errors.Is(listErr, domain.ErrUpstreamUnavailable) {
				t.Fatalf("err = %v, want ErrUpstreamUnavailable", listErr)
			}
			if got != nil || total != 0 {
				t.Errorf("got %v rows, total %d; want nothing on failure", got, total)
			}
		})
	}
}
