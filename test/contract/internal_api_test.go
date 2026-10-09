package contract_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/contract"
)

const internalSpec = "../../api/internal-openapi.yaml"

type upPinger struct{}

func (upPinger) Ping(context.Context) error { return nil }

type upDB struct{}

func (upDB) Health(context.Context) httpadapter.DBHealth { return httpadapter.DBHealth{Healthy: true} }

type contractVersions struct {
	port.WorkflowVersionRepository
	byID map[uuid.UUID]*domain.WorkflowVersion
}

func (c contractVersions) GetByID(_ context.Context, _, id uuid.UUID) (*domain.WorkflowVersion, error) {
	if v, ok := c.byID[id]; ok {
		return v, nil
	}
	return nil, domain.ErrNotFound
}

func internalRouter(t *testing.T, versions ...*domain.WorkflowVersion) http.Handler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := contractVersions{byID: map[uuid.UUID]*domain.WorkflowVersion{}}
	for _, v := range versions {
		repo.byID[v.ID] = v
	}
	plans := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: repo})
	return httpadapter.NewRouter(httpadapter.RouterConfig{
		GinConfig: gincommon.Config{ServiceName: "definition-contract-test", Domain: "workflow", Environment: "test"}, AppEnv: "test", InternalAPIToken: "internal-secret",
		Handler: handler.New(handler.Services{CompiledPlans: plans}),
		DB:      upDB{}, Cache: upPinger{},
	}).Handler()
}

// TestInternalContract_CompiledPlan sends each case through the real router
// and requires the response to be one the internal spec declares.
func TestInternalContract_CompiledPlan(t *testing.T) {
	spec := contract.Load(t, internalSpec)
	tenantID := uuid.New()
	n := int32(4)
	plan := `{"main_plan":"Main","plans":[],"schema_version":1}`
	published := &domain.WorkflowVersion{ID: uuid.New(), WorkflowID: uuid.New(), Status: domain.VersionStatusPublished, IsValid: true, VersionNumber: &n, CompiledPlanJSON: &plan}
	draft := &domain.WorkflowVersion{ID: uuid.New(), WorkflowID: uuid.New(), Status: domain.VersionStatusDraft, IsValid: true}
	router := internalRouter(t, published, draft)

	path := func(versionID, tenant string) string {
		p := "/api/v1/internal/workflow-versions/" + versionID + "/compiled-plan"
		if tenant != "" {
			p += "?tenant_id=" + tenant
		}
		return p
	}
	for _, tc := range []struct {
		name, path string
		token      string
		want       int
		validReq   bool
	}{
		{"published", path(published.ID.String(), tenantID.String()), "internal-secret", http.StatusOK, true},
		{"draft", path(draft.ID.String(), tenantID.String()), "internal-secret", http.StatusOK, true},
		{"unknown version", path(uuid.NewString(), tenantID.String()), "internal-secret", http.StatusNotFound, true},
		{"bad version id", path("not-a-uuid", tenantID.String()), "internal-secret", http.StatusBadRequest, false},
		{"no tenant", path(published.ID.String(), ""), "internal-secret", http.StatusBadRequest, false},
		{"bad tenant", path(published.ID.String(), "nope"), "internal-secret", http.StatusBadRequest, false},
		{"no token", path(published.ID.String(), tenantID.String()), "", http.StatusUnauthorized, true},
		{"wrong token", path(published.ID.String(), tenantID.String()), "guess", http.StatusUnauthorized, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.token != "" {
				req.Header.Set("x-internal-token", tc.token)
			}
			if tc.validReq {
				spec.Request(t, req)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			spec.Response(t, req, w.Code, w.Header(), w.Body.Bytes())
		})
	}
}

func TestInternalContract_CompiledPlanBody(t *testing.T) {
	n := int32(4)
	plan := `{"main_plan":"Main","plans":[],"schema_version":1}`
	published := &domain.WorkflowVersion{ID: uuid.New(), WorkflowID: uuid.New(), Status: domain.VersionStatusPublished, IsValid: true, VersionNumber: &n, CompiledPlanJSON: &plan}
	draft := &domain.WorkflowVersion{ID: uuid.New(), WorkflowID: uuid.New(), Status: domain.VersionStatusDraft}
	router := internalRouter(t, published, draft)

	read := func(v *domain.WorkflowVersion) map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/internal/workflow-versions/"+v.ID.String()+"/compiled-plan?tenant_id="+uuid.NewString(), nil)
		req.Header.Set("x-internal-token", "internal-secret")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v", w.Body.String(), err)
		}
		return body
	}

	got := read(published)
	if got["workflow_id"] != published.WorkflowID.String() || got["version_id"] != published.ID.String() ||
		got["version_number"] != float64(4) || got["status"] != "PUBLISHED" || got["is_valid"] != true {
		t.Errorf("published = %v", got)
	}
	if p, ok := got["compiled_plan"].(map[string]any); !ok || p["main_plan"] != "Main" || p["schema_version"] != float64(1) {
		t.Errorf("compiled_plan = %v, want the stored plan as an object", got["compiled_plan"])
	}

	got = read(draft)
	if got["compiled_plan"] != nil || got["version_number"] != nil {
		t.Errorf("draft = %v, want a null plan and number", got)
	}
}
