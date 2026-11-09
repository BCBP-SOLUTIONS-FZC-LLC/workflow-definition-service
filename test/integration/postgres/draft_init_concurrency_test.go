//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestInitDraft_ConcurrentRequestsForOneWorkflow_OneCreatedOneConflict(t *testing.T) {
	cases := []struct {
		name     string
		requests int
	}{
		{name: "two simultaneous requests", requests: 2},
		{name: "five simultaneous requests", requests: 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)
			wfRepo := postgres.NewWorkflowRepo(pool)
			vRepo := postgres.NewWorkflowVersionRepo(pool)
			ctx := context.Background()
			tenantID, userID := uuid.New(), uuid.New()

			wf := &domain.Workflow{
				ID: uuid.New(), TenantID: tenantID, CreatedByUserID: userID,
				TemplateKey: "concurrent-draft", Name: "Concurrent draft",
			}
			require.NoError(t, wfRepo.Create(ctx, wf))
			published := newDraftVersion(wf.ID, tenantID)
			require.NoError(t, vRepo.Create(ctx, published))
			require.NoError(t, vRepo.Publish(ctx, tenantID, published.ID, 1, 1, `{}`, "hash", nil))
			require.NoError(t, wfRepo.UpdateActiveVersion(ctx, tenantID, wf.ID, &published.ID))

			drafts := service.NewDraftService(service.DraftDeps{Workflows: wfRepo, Versions: vRepo})
			gin.SetMode(gin.TestMode)
			router := gin.New()
			for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
				router.Use(mw)
			}
			router.Use(httpmiddleware.RequireIdentity(nil))
			httpadapter.RegisterAPIRoutes(router.Group("/api/v1"), handler.New(handler.Services{Drafts: drafts}))

			type outcome struct {
				status int
				body   map[string]any
			}
			results := make([]outcome, tc.requests)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := range results {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+wf.ID.String()+"/draft", nil)
					r.Header.Set("x-tenant-id", tenantID.String())
					r.Header.Set("x-user-id", userID.String())
					r.Header.Set("x-tenant-roles", "tenant_admin")
					w := httptest.NewRecorder()
					<-start
					router.ServeHTTP(w, r)
					var body map[string]any
					_ = json.Unmarshal(w.Body.Bytes(), &body)
					results[i] = outcome{status: w.Code, body: body}
				}()
			}
			close(start)
			wg.Wait()

			statuses := make([]int, 0, len(results))
			var createdID any
			for _, res := range results {
				statuses = append(statuses, res.status)
				switch res.status {
				case http.StatusCreated:
					createdID = res.body["version_id"]
					assert.Equal(t, "DRAFT", res.body["status"])
				case http.StatusConflict:
					assert.Equal(t, "DRAFT_ALREADY_EXISTS", res.body["code"])
				}
			}
			sort.Ints(statuses)
			want := make([]int, tc.requests)
			for i := range want {
				want[i] = http.StatusConflict
			}
			want[0] = http.StatusCreated
			sort.Ints(want)
			require.Equal(t, want, statuses)

			draft, err := vRepo.GetDraft(ctx, tenantID, wf.ID)
			require.NoError(t, err)
			assert.Equal(t, createdID, draft.ID.String())
		})
	}
}
