package contract_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

type secretsFake struct{}

func (secretsFake) Write(context.Context, string, map[string]string) error { return nil }
func (secretsFake) Delete(context.Context, string) error                   { return nil }

type draftFake struct{ draft *domain.WorkflowVersion }

func (f draftFake) Get(context.Context, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
	if f.draft == nil {
		return nil, domain.ErrNotFound
	}
	return f.draft, nil
}
func (f draftFake) Init(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
	return f.draft, nil
}
func (f draftFake) Update(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
	return f.draft, nil
}
func (draftFake) Discard(context.Context, uuid.UUID, uuid.UUID) error { return nil }

type starterFake struct{ starter *domain.StarterTemplate }

func (f starterFake) List(context.Context, uuid.UUID, port.StarterFilter) ([]*domain.StarterTemplate, int64, error) {
	return []*domain.StarterTemplate{f.starter}, 1, nil
}
func (f starterFake) Create(context.Context, uuid.UUID, uuid.UUID, service.CreateStarterReq) (*domain.StarterTemplate, error) {
	return f.starter, nil
}
func (f starterFake) Get(context.Context, uuid.UUID, uuid.UUID) (*domain.StarterTemplate, error) {
	return f.starter, nil
}
func (f starterFake) CreateFromWorkflowVersion(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.CreateFromWorkflowVersionReq) (*domain.StarterTemplate, error) {
	return f.starter, nil
}
func (starterFake) Delete(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func TestPublicContract_Catalogue(t *testing.T) {
	spec := contract.Load(t, publicSpec)
	gin.SetMode(gin.TestMode)
	at := time.Now().UTC()
	n := int32(1)
	tenant := uuid.New()
	user := uuid.New()
	draft := &domain.WorkflowVersion{ID: uuid.New(), WorkflowID: uuid.New(), Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>", IsValid: true, CreatedAt: at, UpdatedAt: at, RecordVersion: 3}
	starter := &domain.StarterTemplate{ID: uuid.New(), Scope: domain.ScopeTenant, TenantID: &tenant, Name: "Review", Description: "d", Category: "tender", BPMNXML: "<bpmn/>", CreatedByUserID: &user, RecordVersion: 1, CreatedAt: at, UpdatedAt: at}
	module := &domain.Module{ID: uuid.New(), TenantID: &tenant, Scope: domain.ScopeTenant, Name: "Package", Description: "d", ProcessID: "Process_Package", CreatedByUserID: &user, RecordVersion: 1, CreatedAt: at, UpdatedAt: at}
	moduleVersion := &domain.ModuleVersion{ID: uuid.New(), ModuleID: module.ID, TenantID: &tenant, Scope: domain.ScopeTenant, Status: domain.VersionStatusPublished, BPMNXML: "<bpmn/>", ProcessID: "Process_Package", VersionNumber: &n, IsValid: true, PublishedAt: &at, CreatedByUserID: &user, RecordVersion: 2, CreatedAt: at, UpdatedAt: at}
	router := httpadapter.NewRouter(httpadapter.RouterConfig{
		GinConfig: gincommon.Config{ServiceName: "definition-contract-test", Domain: "workflow", Environment: "test"}, AppEnv: "test", InternalAPIToken: "internal-secret",
		Handler: handler.New(handler.Services{
			Workflows:  createFake{},
			Drafts:     draftFake{draft: draft},
			Starters:   starterFake{starter: starter},
			Modules:    moduleFake{module: module, version: moduleVersion},
			Connectors: service.NewConnectorService(service.ConnectorDeps{Secrets: secretsFake{}}),
			Cache:      freeCache{}, IdempotencyTTL: time.Minute,
		}),
		DB: upDB{}, Cache: upPinger{},
	}).Handler()
	wf := "/api/v1/workflows/" + draft.WorkflowID.String()
	call := func(method, path, body string) *http.Request {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("x-tenant-id", tenant.String())
		req.Header.Set("x-user-id", user.String())
		req.Header.Set("x-tenant-roles", "tenant_admin")
		return req
	}
	xml := strings.Repeat("x", 60)
	for _, tc := range []struct {
		name string
		req  *http.Request
		want int
	}{
		{"get the draft", call(http.MethodGet, wf+"/draft", ""), http.StatusOK},
		{"init the draft", call(http.MethodPost, wf+"/draft", ""), http.StatusCreated},
		{"update the draft", call(http.MethodPut, wf+"/draft", `{"bpmn_xml":"`+xml+`","record_version":3}`), http.StatusOK},
		{"discard the draft", call(http.MethodDelete, wf+"/draft", ""), http.StatusNoContent},
		{"archive a workflow", call(http.MethodPost, wf+"/archive", ""), http.StatusOK},
		{"list starters", call(http.MethodGet, "/api/v1/starters", ""), http.StatusOK},
		{"get a starter", call(http.MethodGet, "/api/v1/starters/"+starter.ID.String(), ""), http.StatusOK},
		{"create a starter", call(http.MethodPost, "/api/v1/starters", `{"name":"Review","bpmn_xml":"`+xml+`"}`), http.StatusCreated},
		{"delete a starter", call(http.MethodDelete, "/api/v1/starters/"+starter.ID.String(), ""), http.StatusOK},
		{"list modules", call(http.MethodGet, "/api/v1/modules", ""), http.StatusOK},
		{"get a module", call(http.MethodGet, "/api/v1/modules/"+module.ID.String(), ""), http.StatusOK},
		{"create a module", call(http.MethodPost, "/api/v1/modules", `{"name":"Package","bpmn_xml":"`+xml+`"}`), http.StatusCreated},
		{"list module versions", call(http.MethodGet, "/api/v1/modules/"+module.ID.String()+"/versions", ""), http.StatusOK},
		{"get a module version", call(http.MethodGet, "/api/v1/modules/"+module.ID.String()+"/versions/"+moduleVersion.ID.String(), ""), http.StatusOK},
		{"get a module contract", call(http.MethodGet, "/api/v1/modules/"+module.ID.String()+"/versions/"+moduleVersion.ID.String()+"/contract", ""), http.StatusOK},
		{"list module usages", call(http.MethodGet, "/api/v1/modules/"+module.ID.String()+"/usages", ""), http.StatusOK},
		{"add a module version", call(http.MethodPost, "/api/v1/modules/"+module.ID.String()+"/versions", `{"bpmn_xml":"`+xml+`"}`), http.StatusCreated},
		{"publish a module version", call(http.MethodPost, "/api/v1/modules/"+module.ID.String()+"/versions/"+moduleVersion.ID.String()+"/publish", ""), http.StatusOK},
		{"archive a module", call(http.MethodDelete, "/api/v1/modules/"+module.ID.String(), ""), http.StatusOK},
		{"write a connector credential", call(http.MethodPost, "/api/v1/connectors/credentials", `{"connector_type":"send-email","field_name":"apiKey","value":"secret"}`), http.StatusCreated},
		{"revoke a connector credential", call(http.MethodDelete, "/api/v1/connectors/credentials/send-email/apiKey", ""), http.StatusNoContent},
		{"module name over its limit", call(http.MethodPost, "/api/v1/modules", `{"name":"`+strings.Repeat("n", 256)+`","bpmn_xml":"`+xml+`"}`), http.StatusUnprocessableEntity},
		{"module description over its limit", call(http.MethodPost, "/api/v1/modules", `{"name":"Package","description":"`+strings.Repeat("d", 2001)+`","bpmn_xml":"`+xml+`"}`), http.StatusUnprocessableEntity},
		{"starter category over its limit", call(http.MethodPost, "/api/v1/starters", `{"name":"Review","category":"`+strings.Repeat("c", 65)+`","bpmn_xml":"`+xml+`"}`), http.StatusUnprocessableEntity},
		{"starter from a version with a long name", call(http.MethodPost, "/api/v1/starters/from-workflow-version/"+uuid.NewString(), `{"name":"`+strings.Repeat("n", 256)+`"}`), http.StatusUnprocessableEntity},
		{"draft name over its limit", call(http.MethodPut, wf+"/draft", `{"name":"`+strings.Repeat("n", 256)+`","record_version":3}`), http.StatusUnprocessableEntity},
		{"draft description over its limit", call(http.MethodPut, wf+"/draft", `{"description":"`+strings.Repeat("d", 2001)+`","record_version":3}`), http.StatusUnprocessableEntity},
		{"list connector types", call(http.MethodGet, "/api/v1/connectors/registry", ""), http.StatusOK},
		{"allowed BPMN elements", call(http.MethodGet, "/api/v1/bpmn/allowed-elements", ""), http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.want != http.StatusUnprocessableEntity {
				spec.Request(t, tc.req)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, tc.req)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			spec.Response(t, tc.req, w.Code, w.Header(), w.Body.Bytes())
			spec.Undeclared(t, tc.req, w.Code, w.Body.Bytes())
		})
	}
}
