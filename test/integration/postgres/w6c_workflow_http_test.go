//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

type w6cLogEntry struct {
	msg    string
	fields map[string]any
}

type w6cInfoLog struct {
	mu      sync.Mutex
	entries []w6cLogEntry
}

func (l *w6cInfoLog) record(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, w6cLogEntry{msg: msg, fields: fields})
}

func (l *w6cInfoLog) Debug(msg string, f map[string]any) { l.record(msg, f) }
func (l *w6cInfoLog) Info(msg string, f map[string]any)  { l.record(msg, f) }
func (l *w6cInfoLog) Warn(msg string, f map[string]any)  { l.record(msg, f) }
func (l *w6cInfoLog) Error(msg string, f map[string]any) { l.record(msg, f) }
func (l *w6cInfoLog) Fatal(msg string, f map[string]any) { l.record(msg, f) }

func (l *w6cInfoLog) named(msg string) []w6cLogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []w6cLogEntry
	for _, e := range l.entries {
		if e.msg == msg {
			out = append(out, e)
		}
	}
	return out
}

func w6cRegisterMetrics(t *testing.T) {
	t.Helper()
	log, err := logger.NewLogger("dev")
	require.NoError(t, err)
	_ = gincommon.ObservabilityMiddlewares(gincommon.Config{
		Logger: log, ServiceName: "workflow-definition-service", BuildVersion: "test",
		Domain: observability.Domain, Environment: "test",
	})
	observability.Register()
}

func w6cRouter(pool *pgcommon.Pool, versionLog *w6cInfoLog) *gin.Engine {
	versions := postgres.NewWorkflowVersionRepo(pool)
	workflows := postgres.NewWorkflowRepo(pool)
	deps := service.VersionDeps{
		Transactor:     postgres.NewTransactor(pool),
		Workflows:      workflows,
		Versions:       versions,
		Assignees:      postgres.NewAssigneeRepo(pool),
		Compiler:       bpmn_compiler.New(),
		ModuleVersions: postgres.NewModuleVersionRepo(pool),
	}
	if versionLog != nil {
		deps.Log = versionLog
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	httpadapter.RegisterAPIRoutes(r.Group("/api/v1"), handler.New(handler.Services{
		Workflows: service.NewWorkflowService(service.WorkflowDeps{Workflows: workflows, Versions: versions}),
		Versions:  service.NewVersionService(deps),
	}))
	return r
}

func w6cCall(router *gin.Engine, method, path string, tenantID, userID uuid.UUID, admin bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", tenantID.String())
	r.Header.Set("x-user-id", userID.String())
	if admin {
		r.Header.Set("x-tenant-roles", "tenant_admin")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

type w6cListResponse struct {
	Workflows  []map[string]any `json:"workflows"`
	Pagination map[string]any   `json:"pagination"`
	raw        string
}

func w6cList(t *testing.T, router *gin.Engine, tenantID uuid.UUID, query string) w6cListResponse {
	t.Helper()
	w := w6cCall(router, http.MethodGet, "/api/v1/workflows"+query, tenantID, uuid.New(), false)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out w6cListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	out.raw = w.Body.String()
	return out
}

func w6cSeedWorkflows(ctx context.Context, t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID, keys ...string) []*domain.Workflow {
	t.Helper()
	repo := postgres.NewWorkflowRepo(pool)
	base := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	out := make([]*domain.Workflow, 0, len(keys))
	for i, key := range keys {
		wf := &domain.Workflow{
			ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(),
			TemplateKey: key, Name: "Name of " + key, Description: "Description of " + key,
		}
		require.NoError(t, repo.Create(ctx, wf))
		at := base.Add(time.Duration(len(keys)-1-i) * time.Hour)
		require.NoError(t, pool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
			tag, err := conn.Exec(ctx, `UPDATE workflow SET created_at = $1 WHERE id = $2`, at, wf.ID)
			if err != nil {
				return err
			}
			require.Equal(t, int64(1), tag.RowsAffected(), "the timestamp update must hit exactly the seeded workflow")
			return nil
		}))
		out = append(out, wf)
	}
	return out
}

func w6cKeys(items []map[string]any) []any {
	keys := make([]any, 0, len(items))
	for _, item := range items {
		keys = append(keys, item["key"])
	}
	return keys
}

func TestListWorkflowsHTTP_ReadsThroughTheRealRepository(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	router := w6cRouter(pool, nil)

	t.Run("is_valid=true keeps only workflows whose active version is valid", func(t *testing.T) {
		tenantID := uuid.New()
		repo := postgres.NewWorkflowRepo(pool)
		valid := createListedWorkflow(ctx, t, repo, tenantID, "w6c-valid", "Valid")
		activateListedWorkflow(ctx, t, pool, valid, true)
		invalid := createListedWorkflow(ctx, t, repo, tenantID, "w6c-invalid", "Invalid")
		activateListedWorkflow(ctx, t, pool, invalid, false)
		createListedWorkflow(ctx, t, repo, tenantID, "w6c-never-published", "Never published")

		got := w6cList(t, router, tenantID, "?is_valid=true")

		assert.Equal(t, []any{"w6c-valid"}, w6cKeys(got.Workflows))
		assert.Equal(t, valid.ID.String(), got.Workflows[0]["id"])
		assert.Equal(t, float64(1), got.Pagination["total_count"])
		assert.Equal(t, []any{"w6c-invalid"}, w6cKeys(w6cList(t, router, tenantID, "?is_valid=false").Workflows))
	})

	t.Run("a tenant with no workflows gets an empty array and a zero total", func(t *testing.T) {
		got := w6cList(t, router, uuid.New(), "")

		assert.Contains(t, got.raw, `"workflows":[]`)
		assert.Equal(t, map[string]any{"total_count": float64(0), "page": float64(1), "limit": float64(20)}, got.Pagination)
	})

	t.Run("no query returns every workflow of the tenant newest first with its fields", func(t *testing.T) {
		tenantID := uuid.New()
		seeded := w6cSeedWorkflows(ctx, t, pool, tenantID, "w6c-a", "w6c-b", "w6c-c")
		w6cSeedWorkflows(ctx, t, pool, uuid.New(), "w6c-other-tenant")

		got := w6cList(t, router, tenantID, "")

		require.Len(t, got.Workflows, 3)
		assert.Equal(t, []any{"w6c-a", "w6c-b", "w6c-c"}, w6cKeys(got.Workflows))
		assert.Equal(t, seeded[0].ID.String(), got.Workflows[0]["id"])
		assert.Equal(t, "Name of w6c-a", got.Workflows[0]["name"])
		assert.Equal(t, "Description of w6c-a", got.Workflows[0]["description"])
		assert.Equal(t, false, got.Workflows[0]["has_draft"])
		assert.NotContains(t, got.Workflows[0], "active_version_id")
		assert.Equal(t, map[string]any{"total_count": float64(3), "page": float64(1), "limit": float64(20)}, got.Pagination)
	})

	t.Run("limit 1 returns one workflow and the full total", func(t *testing.T) {
		tenantID := uuid.New()
		w6cSeedWorkflows(ctx, t, pool, tenantID, "w6c-a", "w6c-b", "w6c-c")

		first := w6cList(t, router, tenantID, "?page=1&limit=1")
		second := w6cList(t, router, tenantID, "?page=2&limit=1")

		assert.Equal(t, []any{"w6c-a"}, w6cKeys(first.Workflows))
		assert.Equal(t, map[string]any{"total_count": float64(3), "page": float64(1), "limit": float64(1)}, first.Pagination)
		assert.Equal(t, []any{"w6c-b"}, w6cKeys(second.Workflows))
		assert.Equal(t, float64(2), second.Pagination["page"])
	})

	t.Run("the largest page and limit give an empty list and keep the total", func(t *testing.T) {
		tenantID := uuid.New()
		w6cSeedWorkflows(ctx, t, pool, tenantID, "w6c-a", "w6c-b", "w6c-c")

		got := w6cList(t, router, tenantID, "?page=100000&limit=100")

		assert.Contains(t, got.raw, `"workflows":[]`)
		assert.Equal(t, map[string]any{"total_count": float64(3), "page": float64(100000), "limit": float64(100)}, got.Pagination)
	})
}

func TestPublishVersionHTTP_ValidDraftByAdmin_PublishesAndStoresThePlanAndHash(t *testing.T) {
	w6cRegisterMetrics(t)
	pool := newTestPool(t)
	ctx := context.Background()
	bpmnXML, err := os.ReadFile("../../unit/service/testdata/single-process.bpmn")
	require.NoError(t, err)
	tenantID, userID := uuid.New(), uuid.New()
	wf, draft := publishRaceSeedDraft(t, pool, tenantID, "w6c-publish-http", string(bpmnXML))
	log := &w6cInfoLog{}
	router := w6cRouter(pool, log)
	path := "/api/v1/workflows/" + wf.ID.String() + "/versions/" + draft.ID.String()
	okBefore := testutil.ToFloat64(observability.WFPublishTotal.WithLabelValues("ok"))
	errBefore := testutil.ToFloat64(observability.WFPublishTotal.WithLabelValues("err"))

	w := w6cCall(router, http.MethodPost, path+"/publish", tenantID, userID, true)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, map[string]any{
		"workflow_id": wf.ID.String(), "version_id": draft.ID.String(),
		"status": "PUBLISHED", "version_number": float64(1), "message": "Version published",
	}, body)
	assert.Equal(t, okBefore+1, testutil.ToFloat64(observability.WFPublishTotal.WithLabelValues("ok")))
	assert.Equal(t, errBefore, testutil.ToFloat64(observability.WFPublishTotal.WithLabelValues("err")))
	published := log.named("version published")
	require.Len(t, published, 1)
	assert.Equal(t, map[string]any{
		"tenant_id": tenantID.String(), "user_id": userID.String(), "workflow_id": wf.ID.String(),
		"version_id": draft.ID.String(), "version_number": int32(1),
	}, published[0].fields)

	read := w6cCall(router, http.MethodGet, path, tenantID, userID, false)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	var stored map[string]any
	require.NoError(t, json.Unmarshal(read.Body.Bytes(), &stored))
	assert.Equal(t, "PUBLISHED", stored["status"])
	assert.Equal(t, float64(1), stored["version_number"])
	publishedAt, isString := stored["published_at"].(string)
	require.True(t, isString, "published_at is a string")
	parsedAt, err := time.Parse(time.RFC3339, publishedAt)
	require.NoError(t, err)
	assert.False(t, parsedAt.IsZero())
	assert.Regexp(t, "^[0-9a-f]{64}$", stored["artifact_hash"])
	assert.Equal(t, []any{}, stored["module_refs"])
	plan, isObject := stored["compiled_plan_json"].(map[string]any)
	require.True(t, isObject, "the stored plan is embedded as a JSON object")
	assert.Equal(t, "Engineering Package Review", plan["main_plan"], "the main plan is named after the fixture's process")

	active, err := postgres.NewWorkflowRepo(pool).GetByID(ctx, tenantID, wf.ID)
	require.NoError(t, err)
	require.NotNil(t, active.ActiveVersionID)
	assert.Equal(t, draft.ID, *active.ActiveVersionID)
}
