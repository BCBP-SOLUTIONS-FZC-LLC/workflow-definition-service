//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func w10aClosedPool(t *testing.T, dsn string) *pgcommon.Pool {
	t.Helper()
	closed, err := pgcommon.NewPool(context.Background(), pgcommon.Config{DSN: dsn})
	require.NoError(t, err)
	closed.Close()
	return closed
}

func w10aBlockedBackends(pool *pgcommon.Pool) (int, error) {
	var n int
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND cardinality(pg_blocking_pids(pid)) > 0`).Scan(&n)
	})
	return n, err
}

type w10aRowLock struct {
	release chan struct{}
	done    chan error
}

func w10aHoldVersionRowLock(t *testing.T, pool *pgcommon.Pool, versionID uuid.UUID) *w10aRowLock {
	t.Helper()
	lock := &w10aRowLock{release: make(chan struct{}), done: make(chan error, 1)}
	held := make(chan struct{})
	go func() {
		lock.done <- pool.WithTx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			var id uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM workflow_version WHERE id = $1 FOR UPDATE`, versionID).Scan(&id); err != nil {
				return err
			}
			close(held)
			<-lock.release
			return nil
		})
	}()
	select {
	case <-held:
	case err := <-lock.done:
		require.FailNow(t, "the lock holder ended before it held the row", "%v", err)
	case <-time.After(time.Minute):
		require.FailNow(t, "the lock holder never took the row lock")
	}
	t.Cleanup(func() {
		select {
		case <-lock.release:
		default:
			close(lock.release)
		}
	})
	return lock
}

func (l *w10aRowLock) finish(t *testing.T) {
	t.Helper()
	select {
	case <-l.release:
	default:
		close(l.release)
	}
	require.NoError(t, <-l.done)
}

func w10aAwaitBlocked(t *testing.T, pool *pgcommon.Pool, what string) {
	t.Helper()
	require.Eventually(t, func() bool {
		n, err := w10aBlockedBackends(pool)
		return err == nil && n > 0
	}, 30*time.Second, 5*time.Millisecond, what)
}

func TestWorkflowVersionRepo_Publish_AWaitingSecondPublishOfTheSameDraft_IsRefusedAsNotDraftOnceTheFirstCommits(t *testing.T) {
	pool := newTestPool(t)
	repo := pgadapter.NewWorkflowVersionRepo(pool)
	transactor := pgadapter.NewTransactor(pool)
	ctx := context.Background()
	tenantID := uuid.New()
	_, draft := publishRaceSeedDraft(t, pool, tenantID, "w10a-publish-loser", "<definitions/>")
	read, err := repo.GetByID(ctx, tenantID, draft.ID)
	require.NoError(t, err)

	held := make(chan struct{})
	release := make(chan struct{})
	winner := make(chan error, 1)
	go func() {
		winner <- transactor.RunInTx(ctx, func(txCtx context.Context) error {
			if err := repo.Publish(txCtx, tenantID, draft.ID, read.RecordVersion, 1, `{"winner":true}`, "w10a-winner", nil); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	select {
	case <-held:
	case err := <-winner:
		require.FailNow(t, "the first publish ended before it held the row", "%v", err)
	case <-time.After(time.Minute):
		require.FailNow(t, "the first publish never updated the row")
	}
	releaseOnce := func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}
	defer releaseOnce()

	loser := make(chan error, 1)
	go func() {
		loser <- repo.Publish(ctx, tenantID, draft.ID, read.RecordVersion, 2, `{"winner":false}`, "w10a-loser", nil)
	}()
	w10aAwaitBlocked(t, pool, "the second publish should wait on the first publish's row lock")
	select {
	case err := <-loser:
		require.FailNow(t, "the second publish finished while the first was uncommitted", "%v", err)
	default:
	}

	releaseOnce()
	require.NoError(t, <-winner)
	loserErr := <-loser

	require.ErrorIs(t, loserErr, domain.ErrVersionNotDraft)
	assert.NotErrorIs(t, loserErr, domain.ErrDraftConcurrency)
	got, err := repo.GetByID(ctx, tenantID, draft.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.VersionStatusPublished, got.Status)
	require.NotNil(t, got.VersionNumber)
	assert.EqualValues(t, 1, *got.VersionNumber)
	assert.Equal(t, "w10a-winner", got.ArtifactHash)
}

func w10aPublishRouter(versionsPool, transactorPool *pgcommon.Pool) *gin.Engine {
	svc := service.NewVersionService(service.VersionDeps{
		PlanLimits:     w7bUnlimitedPlans{},
		Transactor:     pgadapter.NewTransactor(transactorPool),
		Workflows:      pgadapter.NewWorkflowRepo(versionsPool),
		Versions:       pgadapter.NewWorkflowVersionRepo(versionsPool),
		Assignees:      pgadapter.NewAssigneeRepo(versionsPool),
		Compiler:       bpmn_compiler.New(),
		ModuleVersions: pgadapter.NewModuleVersionRepo(versionsPool),
	})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	httpadapter.RegisterAPIRoutes(r.Group("/api/v1"), handler.New(handler.Services{Versions: svc}))
	return r
}

func w10aPublishRequest(ctx context.Context, tenantID, userID, workflowID, versionID uuid.UUID) *http.Request {
	r := httptest.NewRequest(http.MethodPost,
		"/api/v1/workflows/"+workflowID.String()+"/versions/"+versionID.String()+"/publish", strings.NewReader("")).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", tenantID.String())
	r.Header.Set("x-user-id", userID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	r.Header.Set("x-plan", "w10a-plan")
	return r
}

func TestPublishVersionHTTP_DatabaseUnavailable_Answers503AndLeavesTheDraftUnpublished(t *testing.T) {
	pool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	vRepo := pgadapter.NewWorkflowVersionRepo(pool)
	bpmnXML, err := os.ReadFile("../../unit/service/testdata/single-process.bpmn")
	require.NoError(t, err)

	assertUnavailable := func(t *testing.T, w *httptest.ResponseRecorder) {
		t.Helper()
		require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
		assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
		var problem w10aProblem
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &problem))
		assert.Equal(t, "UPSTREAM_UNAVAILABLE", problem.Code)
		assert.Equal(t, "https://api.workflow.platform/errors/service-unavailable", problem.Type)
		assert.Equal(t, "an upstream dependency is temporarily unavailable", problem.Detail)
	}
	assertStillDraft := func(t *testing.T, tenantID, draftID, workflowID uuid.UUID) {
		t.Helper()
		stored, err := vRepo.GetByID(ctx, tenantID, draftID)
		require.NoError(t, err)
		assert.Equal(t, domain.VersionStatusDraft, stored.Status)
		assert.Nil(t, stored.VersionNumber)
		wf, err := pgadapter.NewWorkflowRepo(pool).GetByID(ctx, tenantID, workflowID)
		require.NoError(t, err)
		assert.Nil(t, wf.ActiveVersionID)
	}

	t.Run("the database is down when the draft is read", func(t *testing.T) {
		tenantID, userID := uuid.New(), uuid.New()
		wf, draft := publishRaceSeedDraft(t, pool, tenantID, "w10a-down-read", string(bpmnXML))
		closed := w10aClosedPool(t, dsn)

		w := httptest.NewRecorder()
		w10aPublishRouter(closed, closed).ServeHTTP(w, w10aPublishRequest(ctx, tenantID, userID, wf.ID, draft.ID))

		assertUnavailable(t, w)
		assertStillDraft(t, tenantID, draft.ID, wf.ID)
	})

	t.Run("the database goes away between the read and the publish transaction", func(t *testing.T) {
		tenantID, userID := uuid.New(), uuid.New()
		wf, draft := publishRaceSeedDraft(t, pool, tenantID, "w10a-down-tx", string(bpmnXML))
		closed := w10aClosedPool(t, dsn)

		w := httptest.NewRecorder()
		w10aPublishRouter(pool, closed).ServeHTTP(w, w10aPublishRequest(ctx, tenantID, userID, wf.ID, draft.ID))

		assertUnavailable(t, w)
		assertStillDraft(t, tenantID, draft.ID, wf.ID)
	})

	t.Run("the request is cancelled while the publish update waits on a row lock", func(t *testing.T) {
		tenantID, userID := uuid.New(), uuid.New()
		wf, draft := publishRaceSeedDraft(t, pool, tenantID, "w10a-cancel-update", string(bpmnXML))
		router := w10aPublishRouter(pool, pool)
		lock := w10aHoldVersionRowLock(t, pool, draft.ID)
		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		recorder := httptest.NewRecorder()
		served := make(chan struct{})
		go func() {
			defer close(served)
			router.ServeHTTP(recorder, w10aPublishRequest(reqCtx, tenantID, userID, wf.ID, draft.ID))
		}()
		w10aAwaitBlocked(t, pool, "the publish update should wait on the row lock")
		select {
		case <-served:
			require.FailNow(t, "the publish answered while the draft row was locked", "%s", recorder.Body.String())
		default:
		}

		cancel()
		<-served
		lock.finish(t)

		assertUnavailable(t, recorder)
		assertStillDraft(t, tenantID, draft.ID, wf.ID)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, w10aPublishRequest(ctx, tenantID, userID, wf.ID, draft.ID))
		require.Equal(t, http.StatusOK, w.Code, "control: with the lock gone the same draft publishes: %s", w.Body.String())
	})
}

func TestWorkflowVersionRepo_Publish_QueryCancelledWhileWaitingForARowLock_IsUnavailableAndKeepsTheCause(t *testing.T) {
	pool := newTestPool(t)
	repo := pgadapter.NewWorkflowVersionRepo(pool)
	tenantID := uuid.New()
	_, draft := publishRaceSeedDraft(t, pool, tenantID, "w10a-query-cancel", "<definitions/>")
	read, err := repo.GetByID(context.Background(), tenantID, draft.ID)
	require.NoError(t, err)
	lock := w10aHoldVersionRowLock(t, pool, draft.ID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- repo.Publish(ctx, tenantID, draft.ID, read.RecordVersion, 1, `{}`, "w10a-cancelled", nil)
	}()
	w10aAwaitBlocked(t, pool, "the publish should wait on the row lock")

	cancel()
	err = <-result
	lock.finish(t)

	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, domain.ErrNotFound)
	after, err := repo.GetByID(context.Background(), tenantID, draft.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.VersionStatusDraft, after.Status)
}

func TestVersionRepositories_ClosedOrCancelled_EveryCallIsUnavailableNotNotFound(t *testing.T) {
	pool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	type call struct {
		name string
		run  func(ctx context.Context, pool *pgcommon.Pool) error
	}
	calls := []call{
		{"workflow version GetByID", func(ctx context.Context, p *pgcommon.Pool) error {
			_, err := pgadapter.NewWorkflowVersionRepo(p).GetByID(ctx, uuid.New(), uuid.New())
			return err
		}},
		{"workflow version GetDraft", func(ctx context.Context, p *pgcommon.Pool) error {
			_, err := pgadapter.NewWorkflowVersionRepo(p).GetDraft(ctx, uuid.New(), uuid.New())
			return err
		}},
		{"workflow version Create", func(ctx context.Context, p *pgcommon.Pool) error {
			return pgadapter.NewWorkflowVersionRepo(p).Create(ctx, newDraftVersion(uuid.New(), uuid.New()))
		}},
		{"workflow version Publish", func(ctx context.Context, p *pgcommon.Pool) error {
			return pgadapter.NewWorkflowVersionRepo(p).Publish(ctx, uuid.New(), uuid.New(), 1, 1, `{}`, "h", nil)
		}},
		{"workflow version Archive", func(ctx context.Context, p *pgcommon.Pool) error {
			return pgadapter.NewWorkflowVersionRepo(p).Archive(ctx, uuid.New(), uuid.New())
		}},
		{"workflow version NextVersionNumber", func(ctx context.Context, p *pgcommon.Pool) error {
			_, err := pgadapter.NewWorkflowVersionRepo(p).NextVersionNumber(ctx, uuid.New(), uuid.New())
			return err
		}},
		{"workflow version ListByWorkflow", func(ctx context.Context, p *pgcommon.Pool) error {
			_, _, err := pgadapter.NewWorkflowVersionRepo(p).ListByWorkflow(ctx, uuid.New(), uuid.New(), 1, 20)
			return err
		}},
		{"module version Create", func(ctx context.Context, p *pgcommon.Pool) error {
			return pgadapter.NewModuleVersionRepo(p).Create(ctx, newDraftModuleVersion(uuid.New(), nil, domain.ScopeGlobal))
		}},
		{"module version GetByID", func(ctx context.Context, p *pgcommon.Pool) error {
			_, err := pgadapter.NewModuleVersionRepo(p).GetByID(ctx, uuid.New(), uuid.New())
			return err
		}},
		{"module version Publish", func(ctx context.Context, p *pgcommon.Pool) error {
			return pgadapter.NewModuleVersionRepo(p).Publish(ctx, uuid.New(), uuid.New(), 1)
		}},
		{"module version Archive", func(ctx context.Context, p *pgcommon.Pool) error {
			return pgadapter.NewModuleVersionRepo(p).Archive(ctx, uuid.New(), uuid.New())
		}},
		{"module version NextVersionNumber", func(ctx context.Context, p *pgcommon.Pool) error {
			_, err := pgadapter.NewModuleVersionRepo(p).NextVersionNumber(ctx, uuid.New())
			return err
		}},
		{"module version Resolve", func(ctx context.Context, p *pgcommon.Pool) error {
			_, err := pgadapter.NewModuleVersionRepo(p).Resolve(ctx, uuid.New(), "Process_w10a", 0)
			return err
		}},
	}
	for _, c := range calls {
		t.Run(c.name+" on a closed pool", func(t *testing.T) {
			err := c.run(ctx, w10aClosedPool(t, dsn))

			require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.NotErrorIs(t, err, domain.ErrNotFound)
		})
		t.Run(c.name+" with a cancelled context", func(t *testing.T) {
			err := c.run(cancelled, pool)

			require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.ErrorIs(t, err, context.Canceled)
		})
		t.Run(c.name+" on the live pool does not report an outage", func(t *testing.T) {
			err := c.run(ctx, pool)

			assert.NotErrorIs(t, err, domain.ErrUpstreamUnavailable)
		})
	}
}
