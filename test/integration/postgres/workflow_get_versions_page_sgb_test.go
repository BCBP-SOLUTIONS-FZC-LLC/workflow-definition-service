//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func sgbWorkflowRouter(t *testing.T, pool *pgcommon.Pool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Workflows: postgres.NewWorkflowRepo(pool),
		Versions:  postgres.NewWorkflowVersionRepo(pool),
	})
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	httpadapter.RegisterAPIRoutes(r.Group("/api/v1"), handler.New(handler.Services{Workflows: svc}))
	return r
}

func sgbGetWorkflow(r *gin.Engine, tenantID, workflowID uuid.UUID, query string) *httptest.ResponseRecorder {
	target := "/api/v1/workflows/" + workflowID.String() + query
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("x-tenant-id", tenantID.String())
	request.Header.Set("x-user-id", uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, request)
	return w
}

func sgbSeedArchivedVersions(
	t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID, count int,
) (workflowID uuid.UUID, idByNumber map[int32]uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(),
		TemplateKey: "sgb-" + uuid.NewString(), Name: "Paged versions",
	}
	require.NoError(t, postgres.NewWorkflowRepo(pool).Create(ctx, wf))

	versions := postgres.NewWorkflowVersionRepo(pool)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	idByNumber = make(map[int32]uuid.UUID, count)
	for n := int32(1); n <= int32(count); n++ {
		number := n
		v := newDraftVersion(wf.ID, tenantID)
		v.Status = domain.VersionStatusArchived
		v.VersionNumber = &number
		require.NoError(t, versions.Create(ctx, v))
		err := pool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
			_, err := conn.Exec(ctx, `UPDATE workflow_version SET created_at = $2 WHERE id = $1`,
				v.ID, base.Add(time.Duration(n)*time.Hour))
			return err
		})
		require.NoError(t, err)
		idByNumber[n] = v.ID
	}
	return wf.ID, idByNumber
}

type sgbWorkflowBody struct {
	ID       uuid.UUID `json:"id"`
	Versions []struct {
		ID            uuid.UUID `json:"id"`
		VersionNumber int32     `json:"version_number"`
		Status        string    `json:"status"`
	} `json:"versions"`
}

func TestGetWorkflow_StoredVersions_PageIsNewestFirstAndSizedByVersionsLimit(t *testing.T) {
	const stored = 25
	pool := newTestPool(t)
	tenantID := uuid.New()
	workflowID, idByNumber := sgbSeedArchivedVersions(t, pool, tenantID, stored)
	router := sgbWorkflowRouter(t, pool)

	newestFirst := func(n int) []int32 {
		out := make([]int32, 0, n)
		for number := int32(stored); len(out) < n; number-- {
			out = append(out, number)
		}
		return out
	}
	cases := []struct {
		name  string
		query string
		want  []int32
	}{
		{name: "no versions_limit gives the default of twenty", query: "", want: newestFirst(20)},
		{name: "versions_limit 5", query: "?versions_limit=5", want: newestFirst(5)},
		{name: "versions_limit 1 lower bound", query: "?versions_limit=1", want: newestFirst(1)},
		{name: "versions_limit 100 upper bound returns all stored", query: "?versions_limit=100", want: newestFirst(stored)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := sgbGetWorkflow(router, tenantID, workflowID, tc.query)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var body sgbWorkflowBody
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, workflowID, body.ID)
			got := make([]int32, 0, len(body.Versions))
			for _, v := range body.Versions {
				got = append(got, v.VersionNumber)
				assert.Equal(t, idByNumber[v.VersionNumber], v.ID)
				assert.Equal(t, "ARCHIVED", v.Status)
			}
			assert.Equal(t, tc.want, got, "newest created version first, cut at the requested size")
		})
	}
}

func TestGetWorkflow_StoredVersions_OutOfRangeLimitIsRejectedAs400(t *testing.T) {
	pool := newTestPool(t)
	tenantID := uuid.New()
	workflowID, _ := sgbSeedArchivedVersions(t, pool, tenantID, 3)
	router := sgbWorkflowRouter(t, pool)

	for _, query := range []string{"?versions_limit=0", "?versions_limit=101", "?versions_limit=abc", "?versions_limit=", "?versions_limit=1.5"} {
		t.Run(query, func(t *testing.T) {
			w := sgbGetWorkflow(router, tenantID, workflowID, query)

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			var prob struct {
				Code          string `json:"code"`
				InvalidParams []struct {
					Name string `json:"name"`
				} `json:"invalid_params"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &prob))
			assert.Equal(t, "INVALID_INPUT", prob.Code)
			require.Len(t, prob.InvalidParams, 1)
			assert.Equal(t, "versions_limit", prob.InvalidParams[0].Name)
		})
	}
}

func TestWorkflowVersionRepo_ListByWorkflow_MapsEachRowIntoASummaryWithoutTheHeavyColumns(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(),
		TemplateKey: "sgb-" + uuid.NewString(), Name: "Summary mapping",
	}
	require.NoError(t, postgres.NewWorkflowRepo(pool).Create(ctx, wf))

	versions := postgres.NewWorkflowVersionRepo(pool)
	number := int32(1)
	publishedAt := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)
	plan := `{"nodes":[]}`
	published := newDraftVersion(wf.ID, tenantID)
	published.Status = domain.VersionStatusPublished
	published.VersionNumber = &number
	published.PublishedAt = &publishedAt
	published.CompiledPlanJSON = &plan
	published.ArtifactHash = "hash-1"
	published.BPMNXML = "<definitions>published body</definitions>"
	require.NoError(t, versions.Create(ctx, published))
	draft := newDraftVersion(wf.ID, tenantID)
	draft.IsValid = false
	draft.BPMNXML = "<definitions>draft body</definitions>"
	require.NoError(t, versions.Create(ctx, draft))

	got, total, err := versions.ListByWorkflow(ctx, tenantID, wf.ID, 1, 20)

	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, got, 2)
	byID := map[uuid.UUID]*domain.WorkflowVersion{}
	for _, v := range got {
		byID[v.ID] = v
		assert.Equal(t, wf.ID, v.WorkflowID)
		assert.Equal(t, tenantID, v.TenantID)
		assert.Empty(t, v.BPMNXML, "a page of versions never reads the BPMN")
		assert.Nil(t, v.CompiledPlanJSON, "a page of versions never reads the compiled plan")
		assert.False(t, v.CreatedAt.IsZero())
		assert.False(t, v.UpdatedAt.IsZero())
	}
	gotPublished, gotDraft := byID[published.ID], byID[draft.ID]
	require.NotNil(t, gotPublished)
	require.NotNil(t, gotDraft)
	assert.Equal(t, domain.VersionStatusPublished, gotPublished.Status)
	require.NotNil(t, gotPublished.VersionNumber)
	assert.Equal(t, number, *gotPublished.VersionNumber)
	require.NotNil(t, gotPublished.PublishedAt)
	assert.True(t, publishedAt.Equal(*gotPublished.PublishedAt))
	assert.Equal(t, published.CreatedByUserID, gotPublished.CreatedByUserID)
	assert.True(t, gotPublished.IsValid)
	assert.Equal(t, domain.VersionStatusDraft, gotDraft.Status)
	assert.Nil(t, gotDraft.VersionNumber)
	assert.Nil(t, gotDraft.PublishedAt)
	assert.False(t, gotDraft.IsValid)
	assert.Equal(t, draft.CreatedByUserID, gotDraft.CreatedByUserID)
}
