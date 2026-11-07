//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func TestWorkflowVersionRepo_GetDraft_OtherTenantsWorkflow_ReturnsNotFound(t *testing.T) {
	cases := []struct {
		name       string
		askAsOwner bool
		pool       func(t *testing.T, superPool *pgcommon.Pool, dsn string, owner, caller uuid.UUID) *pgcommon.Pool
	}{
		{
			name: "query filters on the caller's tenant",
			pool: func(_ *testing.T, superPool *pgcommon.Pool, _ string, _, _ uuid.UUID) *pgcommon.Pool {
				return superPool
			},
		},
		{
			name:       "row-level security hides the other tenant's rows even when the query names the owner",
			askAsOwner: true,
			pool: func(t *testing.T, _ *pgcommon.Pool, dsn string, _, caller uuid.UUID) *pgcommon.Pool {
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

			wf := &domain.Workflow{
				ID: uuid.New(), TenantID: owner,
				CreatedByUserID: uuid.New(), TemplateKey: "wv-draft-isolation", Name: "X",
			}
			if err := postgres.NewWorkflowRepo(superPool).Create(ctx, wf); err != nil {
				t.Fatalf("create workflow: %v", err)
			}
			draft := newDraftVersion(wf.ID, owner)
			if err := postgres.NewWorkflowVersionRepo(superPool).Create(ctx, draft); err != nil {
				t.Fatalf("create draft: %v", err)
			}

			callerRepo := postgres.NewWorkflowVersionRepo(tc.pool(t, superPool, dsn, owner, caller))
			queryTenant := caller
			if tc.askAsOwner {
				queryTenant = owner
			}
			got, err := callerRepo.GetDraft(ctx, queryTenant, wf.ID)
			if !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("GetDraft as another tenant = %v, %v; want ErrNotFound", got, err)
			}

			own, err := postgres.NewWorkflowVersionRepo(superPool).GetDraft(ctx, owner, wf.ID)
			if err != nil || own.ID != draft.ID {
				t.Errorf("owner GetDraft = %v, %v; want the draft %s", own, err, draft.ID)
			}
		})
	}
}

func TestWorkflowVersionRepo_GetDraft_DatabaseUnavailable_ReturnsUpstreamUnavailable(t *testing.T) {
	cases := []struct {
		name  string
		setup func(pool *pgcommon.Pool) (context.Context, context.CancelFunc)
	}{
		{
			name: "pool closed",
			setup: func(pool *pgcommon.Pool) (context.Context, context.CancelFunc) {
				pool.Close()
				return context.WithCancel(context.Background())
			},
		},
		{
			name: "request deadline already exceeded",
			setup: func(*pgcommon.Pool) (context.Context, context.CancelFunc) {
				return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)
			repo := postgres.NewWorkflowVersionRepo(pool)
			ctx, cancel := tc.setup(pool)
			defer cancel()

			_, err := repo.GetDraft(ctx, uuid.New(), uuid.New())
			if !errors.Is(err, domain.ErrUpstreamUnavailable) {
				t.Errorf("GetDraft = %v, want ErrUpstreamUnavailable so the API answers 503", err)
			}
		})
	}
}

func TestWorkflowVersionRepo_GetDraft_ConnectionLostMidTransaction_ReturnsUpstreamUnavailable(t *testing.T) {
	pool, dsn := fixtures.NewTestPoolAndDSN(t)
	repo := postgres.NewWorkflowVersionRepo(pool)
	transactor := postgres.NewTransactor(pool)
	admin, err := pgcommon.NewPool(context.Background(), pgcommon.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("create admin pool: %v", err)
	}
	t.Cleanup(admin.Close)

	var got error
	_ = transactor.RunInTx(context.Background(), func(txCtx context.Context) error {
		terminateOtherBackends(t, admin)
		_, got = repo.GetDraft(txCtx, uuid.New(), uuid.New())
		return got
	})

	if !errors.Is(got, domain.ErrUpstreamUnavailable) {
		t.Errorf("GetDraft after the connection was terminated = %v, want ErrUpstreamUnavailable", got)
	}
}

func terminateOtherBackends(t *testing.T, admin *pgcommon.Pool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var remaining int
		err := admin.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
			if _, err := conn.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
				WHERE datname = current_database() AND backend_type = 'client backend' AND pid <> pg_backend_pid()`); err != nil {
				return err
			}
			return conn.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
				WHERE datname = current_database() AND backend_type = 'client backend'
				AND pid <> pg_backend_pid()`).Scan(&remaining)
		})
		if err != nil {
			t.Fatalf("terminate backends: %v", err)
		}
		if remaining == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d backends still connected after pg_terminate_backend", remaining)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
