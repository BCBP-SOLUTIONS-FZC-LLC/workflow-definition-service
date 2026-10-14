package contract_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/contract"
)

const publicSpec = "../../docs/swagger/openapi.yaml"

type workflowFake interface {
	List(context.Context, uuid.UUID, port.WorkflowFilter) ([]*domain.Workflow, int64, error)
	Create(context.Context, uuid.UUID, uuid.UUID, string, string, string, string, string) (*domain.Workflow, *domain.WorkflowVersion, error)
	Get(context.Context, uuid.UUID, uuid.UUID, int) (*domain.Workflow, []*domain.WorkflowVersion, error)
	Archive(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error
}

type createFake struct{ err error }

func (createFake) List(context.Context, uuid.UUID, port.WorkflowFilter) ([]*domain.Workflow, int64, error) {
	return nil, 0, nil
}

func (f createFake) Create(_ context.Context, tenantID, _ uuid.UUID, key, name, description, _, _ string) (*domain.Workflow, *domain.WorkflowVersion, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	wf := &domain.Workflow{ID: uuid.New(), TenantID: tenantID, BusinessKey: key, Name: name, Description: description}
	return wf, &domain.WorkflowVersion{ID: uuid.New(), WorkflowID: wf.ID, Status: domain.VersionStatusDraft}, nil
}

func (createFake) Get(context.Context, uuid.UUID, uuid.UUID, int) (*domain.Workflow, []*domain.WorkflowVersion, error) {
	return nil, nil, domain.ErrNotFound
}

func (createFake) Archive(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error { return nil }

type busyCache struct{ port.CacheStore }

func (busyCache) SetNX(context.Context, string, string, time.Duration) (bool, error) {
	return false, nil
}

func (busyCache) Get(context.Context, string) (string, error) { return "", nil }

type freeCache struct{ port.CacheStore }

func (freeCache) SetNX(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}

func (freeCache) Get(context.Context, string) (string, error)              { return "", nil }
func (freeCache) Set(context.Context, string, string, time.Duration) error { return nil }
func (freeCache) DelIfEqual(context.Context, string, string) error         { return nil }

type downPinger struct{}

func (downPinger) Ping(context.Context) error { return context.DeadlineExceeded }

type downDB struct{}

func (downDB) Health(context.Context) httpadapter.DBHealth { return httpadapter.DBHealth{} }

func publicRouter(t *testing.T, wf workflowFake, cache port.CacheStore, db httpadapter.DBPinger, pinger httpadapter.Pinger) http.Handler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	return httpadapter.NewRouter(httpadapter.RouterConfig{
		GinConfig: gincommon.Config{ServiceName: "definition-contract-test", Domain: "workflow", Environment: "test"}, AppEnv: "test", InternalAPIToken: "internal-secret",
		Handler: handler.New(handler.Services{Workflows: wf, Cache: cache, IdempotencyTTL: time.Minute}),
		DB:      db, Cache: pinger,
	}).Handler()
}

func createRequest(key string) *http.Request {
	body := `{"key":"flow-one","name":"Flow one","bpmn_xml":"` + strings.Repeat("x", 60) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-tenant-id", uuid.NewString())
	req.Header.Set("x-user-id", uuid.NewString())
	req.Header.Set("x-tenant-roles", "tenant_admin")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	return req
}

func listRequest(query string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows"+query, nil)
	req.Header.Set("x-tenant-id", uuid.NewString())
	req.Header.Set("x-user-id", uuid.NewString())
	return req
}

func TestPublicContract_Responses(t *testing.T) {
	spec := contract.Load(t, publicSpec)
	free := publicRouter(t, createFake{}, freeCache{}, upDB{}, upPinger{})
	busy := publicRouter(t, createFake{}, busyCache{}, upDB{}, upPinger{})
	outage := publicRouter(t, createFake{err: domain.ErrUpstreamUnavailable}, freeCache{}, upDB{}, upPinger{})

	for _, tc := range []struct {
		name   string
		router http.Handler
		req    *http.Request
		want   int
		valid  bool
	}{
		{"created", free, createRequest("k-1"), http.StatusCreated, true},
		{"key reused while the first request runs", busy, createRequest("k-2"), http.StatusConflict, true},
		{"key longer than 256 characters", free, createRequest(strings.Repeat("k", 257)), http.StatusBadRequest, false},
		{"page out of range", free, listRequest("?page=0"), http.StatusBadRequest, false},
		{"limit out of range", free, listRequest("?limit=500"), http.StatusBadRequest, false},
		{"first page", free, listRequest("?page=1&limit=100"), http.StatusOK, true},
		{"database outage", outage, createRequest(""), http.StatusServiceUnavailable, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.valid {
				spec.Request(t, tc.req)
			}
			w := httptest.NewRecorder()
			tc.router.ServeHTTP(w, tc.req)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			spec.Response(t, tc.req, w.Code, w.Header(), w.Body.Bytes())
			spec.Undeclared(t, tc.req, w.Code, w.Body.Bytes())
		})
	}
}

func TestPublicContract_Readiness(t *testing.T) {
	spec := contract.Load(t, publicSpec)
	for _, tc := range []struct {
		name   string
		router http.Handler
		want   int
		cache  string
	}{
		{"ready", publicRouter(t, createFake{}, freeCache{}, upDB{}, upPinger{}), http.StatusOK, "ok"},
		{"ready with the cache down", publicRouter(t, createFake{}, freeCache{}, upDB{}, downPinger{}), http.StatusOK, "degraded"},
		{"database down", publicRouter(t, createFake{}, freeCache{}, downDB{}, upPinger{}), http.StatusServiceUnavailable, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			w := httptest.NewRecorder()
			tc.router.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			spec.Response(t, req, w.Code, w.Header(), w.Body.Bytes())
			spec.Undeclared(t, req, w.Code, w.Body.Bytes())
			if tc.cache == "" {
				return
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["cache"] != tc.cache {
				t.Errorf("body %s, want cache %q", w.Body.String(), tc.cache)
			}
		})
	}
}

type countingList struct {
	createFake
	calls *int
}

func (c countingList) List(context.Context, uuid.UUID, port.WorkflowFilter) ([]*domain.Workflow, int64, error) {
	*c.calls++
	return nil, 0, nil
}

func TestPublicContract_MalformedIdentityIsRefusedBeforeTheService(t *testing.T) {
	spec := contract.Load(t, publicSpec)
	for _, tc := range []struct{ name, tenant, user string }{
		{"tenant is not a uuid", "x", uuid.NewString()},
		{"user is not a uuid", uuid.NewString(), "x"},
		{"tenant is missing", "", uuid.NewString()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			router := publicRouter(t, countingList{calls: &calls}, freeCache{}, upDB{}, upPinger{})
			req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows", nil)
			req.Header.Set("x-tenant-id", tc.tenant)
			req.Header.Set("x-user-id", tc.user)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401: %s", w.Code, w.Body.String())
			}
			if calls != 0 {
				t.Errorf("the service was called %d times", calls)
			}
			spec.Response(t, req, w.Code, w.Header(), w.Body.Bytes())
			spec.Undeclared(t, req, w.Code, w.Body.Bytes())
		})
	}
}

type readFake struct {
	createFake
	workflow *domain.Workflow
	versions []*domain.WorkflowVersion
}

func (f readFake) Get(_ context.Context, _ uuid.UUID, id uuid.UUID, _ int) (*domain.Workflow, []*domain.WorkflowVersion, error) {
	if f.workflow == nil || f.workflow.ID != id {
		return nil, nil, domain.ErrNotFound
	}
	return f.workflow, f.versions, nil
}

func TestPublicContract_GetWorkflow(t *testing.T) {
	spec := contract.Load(t, publicSpec)
	tenant := uuid.New()
	n := int32(2)
	published := time.Now().UTC()
	wf := &domain.Workflow{ID: uuid.New(), TenantID: tenant, BusinessKey: "flow-one", Name: "Flow one", Description: "d", CreatedByUserID: uuid.New(), CreatedAt: published, UpdatedAt: published}
	versions := []*domain.WorkflowVersion{
		{ID: uuid.New(), WorkflowID: wf.ID, Status: domain.VersionStatusPublished, VersionNumber: &n, IsValid: true, CreatedAt: published, PublishedAt: &published},
		{ID: uuid.New(), WorkflowID: wf.ID, Status: domain.VersionStatusDraft, IsValid: true, CreatedAt: published},
	}
	router := publicRouter(t, readFake{workflow: wf, versions: versions}, freeCache{}, upDB{}, upPinger{})

	for _, tc := range []struct {
		name string
		id   uuid.UUID
		want int
	}{
		{"found", wf.ID, http.StatusOK},
		{"unknown", uuid.New(), http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/"+tc.id.String(), nil)
			req.Header.Set("x-tenant-id", tenant.String())
			req.Header.Set("x-user-id", uuid.NewString())
			spec.Request(t, req)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			spec.Response(t, req, w.Code, w.Header(), w.Body.Bytes())
			spec.Undeclared(t, req, w.Code, w.Body.Bytes())
		})
	}
}

type versionFake struct {
	version    *domain.WorkflowVersion
	workflow   *domain.Workflow
	diff       *service.DiffResult
	nodes      *service.VersionNodes
	publishErr error
	cloneErr   error
}

func (f versionFake) List(context.Context, uuid.UUID, uuid.UUID, int, int) ([]*domain.WorkflowVersion, int64, error) {
	return []*domain.WorkflowVersion{f.version}, 1, nil
}

func (f versionFake) Get(_ context.Context, _, _, id uuid.UUID) (*domain.WorkflowVersion, error) {
	if f.version == nil || f.version.ID != id {
		return nil, domain.ErrNotFound
	}
	return f.version, nil
}

func (f versionFake) Publish(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, bool) (*domain.WorkflowVersion, error) {
	return f.version, f.publishErr
}

func (f versionFake) Clone(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string, service.CloneReq) (*domain.Workflow, *domain.WorkflowVersion, error) {
	return f.workflow, f.version, f.cloneErr
}

func (f versionFake) Promote(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
	return f.version, nil
}

func (f versionFake) Export(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (string, string, error) {
	if f.version == nil {
		return "", "", domain.ErrNotFound
	}
	return f.version.BPMNXML, "flow-v1.bpmn", nil
}

func (f versionFake) Diff(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (*service.DiffResult, error) {
	if f.diff == nil {
		return nil, domain.ErrNotFound
	}
	return f.diff, nil
}

func (f versionFake) Nodes(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*service.VersionNodes, error) {
	if f.nodes == nil {
		return nil, domain.ErrNotFound
	}
	return f.nodes, nil
}

func TestPublicContract_Versions(t *testing.T) {
	spec := contract.Load(t, publicSpec)
	gin.SetMode(gin.TestMode)
	n := int32(3)
	at := time.Now().UTC()
	version := &domain.WorkflowVersion{ID: uuid.New(), WorkflowID: uuid.New(), Status: domain.VersionStatusPublished, VersionNumber: &n, BPMNXML: "<bpmn/>", IsValid: true, CreatedAt: at, PublishedAt: &at, RecordVersion: 2}
	workflow := &domain.Workflow{ID: uuid.New(), BusinessKey: "flow-copy", Name: "Flow copy", CreatedAt: at, UpdatedAt: at}
	diff := &service.DiffResult{WorkflowID: version.WorkflowID, BaseVersionID: version.ID, TargetVersionID: uuid.New(), ChangeType: service.ChangeTypeStructural, Changes: service.DiffChanges{
		AddedDepartments: []string{"legal"}, StageChanges: []service.StageChange{{NodeKey: "legal/Review", Description: "added"}},
		StepChanges: []service.StepChange{{Description: "step added", After: &dsl.ExecutionStep{Sequential: []string{"legal"}}}},
	}}
	nodes := &service.VersionNodes{Version: version,
		Nodes: []service.PlanNode{{Key: "eng/Review", NodeID: "Review", Name: "Review", StageType: "review", Role: "reviewer", DepartmentID: "eng", DepartmentLabel: "Engineering", IAMDepartmentID: uuid.NewString(), DefaultUserIDs: []string{uuid.NewString()}}},
		Waits: []service.PlanWait{{Key: "eng/Wait", Kind: service.WaitReceiveTask, Message: "answer"}}}
	router := func(f versionFake) http.Handler {
		return httpadapter.NewRouter(httpadapter.RouterConfig{
			GinConfig: gincommon.Config{ServiceName: "definition-contract-test", Domain: "workflow", Environment: "test"}, AppEnv: "test", InternalAPIToken: "internal-secret",
			Handler: handler.New(handler.Services{Versions: f, Cache: freeCache{}, IdempotencyTTL: time.Minute}),
			DB:      upDB{}, Cache: upPinger{},
		}).Handler()
	}
	base := "/api/v1/workflows/" + version.WorkflowID.String() + "/versions"
	post := func(path, body string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-tenant-id", uuid.NewString())
		req.Header.Set("x-user-id", uuid.NewString())
		req.Header.Set("x-tenant-roles", "tenant_admin")
		return req
	}
	get := func(path string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("x-tenant-id", uuid.NewString())
		req.Header.Set("x-user-id", uuid.NewString())
		return req
	}

	for _, tc := range []struct {
		name   string
		router http.Handler
		req    *http.Request
		want   int
	}{
		{"list versions", router(versionFake{version: version}), get(base), http.StatusOK},
		{"get version", router(versionFake{version: version}), get(base + "/" + version.ID.String()), http.StatusOK},
		{"get unknown version", router(versionFake{version: version}), get(base + "/" + uuid.NewString()), http.StatusNotFound},
		{"publish", router(versionFake{version: version}), post(base+"/"+version.ID.String()+"/publish", `{}`), http.StatusOK},
		{"publish over a structural divergence", router(versionFake{version: version, publishErr: domain.ErrStructuralDivergence}), post(base+"/"+version.ID.String()+"/publish", `{}`), http.StatusConflict},
		{"clone", router(versionFake{version: version, workflow: workflow}), post(base+"/"+version.ID.String()+"/clone", `{"new_key":"flow-copy","new_name":"Flow copy"}`), http.StatusCreated},
		{"promote", router(versionFake{version: version}), post(base+"/"+version.ID.String()+"/promote", ``), http.StatusOK},
		{"export", router(versionFake{version: version}), get(base + "/" + version.ID.String() + "/export"), http.StatusOK},
		{"export an unknown version", router(versionFake{}), get(base + "/" + version.ID.String() + "/export"), http.StatusNotFound},
		{"diff", router(versionFake{version: version, diff: diff}), get(base + "/" + version.ID.String() + "/diff/" + uuid.NewString()), http.StatusOK},
		{"nodes", router(versionFake{version: version, nodes: nodes}), get(base + "/" + version.ID.String() + "/nodes"), http.StatusOK},
		{"clone with an incomplete body", router(versionFake{version: version}), post(base+"/"+version.ID.String()+"/clone", `{}`), http.StatusBadRequest},
		{"promote with a malformed version id", router(versionFake{version: version}), post(base+"/not-a-uuid/promote", ``), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.router.ServeHTTP(w, tc.req)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			spec.Response(t, tc.req, w.Code, w.Header(), w.Body.Bytes())
			spec.Undeclared(t, tc.req, w.Code, w.Body.Bytes())
		})
	}
}

type moduleFake struct {
	module  *domain.Module
	version *domain.ModuleVersion
}

func (f moduleFake) List(context.Context, uuid.UUID, port.ModuleFilter) ([]*domain.Module, int64, error) {
	return []*domain.Module{f.module}, 1, nil
}
func (f moduleFake) Create(context.Context, uuid.UUID, uuid.UUID, service.CreateModuleReq) (*domain.Module, *domain.ModuleVersion, error) {
	return f.module, f.version, nil
}
func (f moduleFake) Get(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
	return f.module, f.version, nil
}
func (f moduleFake) ListVersions(context.Context, uuid.UUID, uuid.UUID, int, int) ([]*domain.ModuleVersion, int64, error) {
	return []*domain.ModuleVersion{f.version}, 1, nil
}
func (f moduleFake) GetVersion(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.ModuleVersion, error) {
	return f.version, nil
}
func (f moduleFake) AddVersion(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.AddModuleVersionReq) (*domain.ModuleVersion, error) {
	return f.version, nil
}
func (f moduleFake) Publish(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.ModuleVersion, error) {
	return f.version, nil
}
func (moduleFake) Archive(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error { return nil }
func (moduleFake) Usages(context.Context, uuid.UUID, uuid.UUID) ([]*domain.ModuleUsage, error) {
	return []*domain.ModuleUsage{{WorkflowID: uuid.New(), WorkflowKey: "k", WorkflowName: "n", VersionID: uuid.New(), Status: domain.VersionStatusPublished, ModuleVersionNumber: 1}}, nil
}
func (f moduleFake) Contract(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*service.ModuleContract, error) {
	return &service.ModuleContract{Version: f.version, Slots: []service.ModuleSlot{{}}, Tasks: []service.ModuleTask{{}}, Messages: []string{"m"}, ErrorCodes: []string{"e"}}, nil
}

func TestPublicContract_BodyRoutesDeclareTheirRefusals(t *testing.T) {
	spec := contract.Load(t, publicSpec)
	gin.SetMode(gin.TestMode)
	router := httpadapter.NewRouter(httpadapter.RouterConfig{
		GinConfig: gincommon.Config{ServiceName: "definition-contract-test", Domain: "workflow", Environment: "test"}, AppEnv: "test", InternalAPIToken: "internal-secret",
		Handler: handler.New(handler.Services{Workflows: createFake{}, Modules: moduleFake{module: &domain.Module{ID: uuid.New(), Scope: domain.ScopeTenant}}, Cache: freeCache{}, IdempotencyTTL: time.Minute}),
		DB:      upDB{}, Cache: upPinger{},
	}).Handler()
	id := regexp.MustCompile(`\{[^}]+\}`)
	for _, op := range spec.Operations() {
		if !op.HasBody {
			continue
		}
		path := id.ReplaceAllString(op.Path, uuid.NewString())
		for _, tc := range []struct {
			name, contentType, body string
			want                    int
		}{
			{"wrong media type", "text/plain", `{}`, http.StatusUnsupportedMediaType},
			{"oversized body", "application/json", `{"bpmn_xml":"` + strings.Repeat("x", 11<<20) + `"}`, http.StatusRequestEntityTooLarge},
		} {
			t.Run(op.Method+" "+op.Path+" "+tc.name, func(t *testing.T) {
				req := httptest.NewRequest(op.Method, path, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", tc.contentType)
				req.Header.Set("x-tenant-id", uuid.NewString())
				req.Header.Set("x-user-id", uuid.NewString())
				req.Header.Set("x-tenant-roles", "tenant_admin")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code != tc.want {
					t.Fatalf("status %d, want %d: %.200s", w.Code, tc.want, w.Body.String())
				}
				spec.Response(t, req, w.Code, w.Header(), w.Body.Bytes())
				spec.Undeclared(t, req, w.Code, w.Body.Bytes())
			})
		}
	}
}

type validationFake struct{ issues []domain.BPMNValidationError }

func (f validationFake) Validate(context.Context, uuid.UUID, string) (bool, []domain.BPMNValidationError, error) {
	return len(f.issues) == 0, f.issues, nil
}
func (f validationFake) ValidateVersion(context.Context, uuid.UUID, *domain.WorkflowVersion) (bool, []domain.BPMNValidationError, error) {
	return len(f.issues) == 0, f.issues, nil
}

func TestPublicContract_Validation(t *testing.T) {
	spec := contract.Load(t, publicSpec)
	gin.SetMode(gin.TestMode)
	version := &domain.WorkflowVersion{ID: uuid.New(), WorkflowID: uuid.New(), Status: domain.VersionStatusDraft, RecordVersion: 1}
	issues := []domain.BPMNValidationError{
		{NodeID: "Task_1", Code: domain.BPMNWarnUnknownConnectorType, Message: "unknown connector", Severity: domain.SeverityWarning},
		{Code: domain.BPMNWarnUnknownConnectorType, Message: "no node", Severity: domain.SeverityError},
	}
	body := `{"bpmn_xml":"` + strings.Repeat("x", 60) + `"}`
	for _, tc := range []struct {
		name   string
		issues []domain.BPMNValidationError
		method string
		path   string
	}{
		{"validate a clean document", nil, http.MethodPost, "/api/v1/workflows/validate"},
		{"validate a document with issues", issues, http.MethodPost, "/api/v1/workflows/validate"},
		{"stored version with issues", issues, http.MethodGet, "/api/v1/workflows/" + version.WorkflowID.String() + "/versions/" + version.ID.String() + "/validation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := httpadapter.NewRouter(httpadapter.RouterConfig{
				GinConfig: gincommon.Config{ServiceName: "definition-contract-test", Domain: "workflow", Environment: "test"}, AppEnv: "test", InternalAPIToken: "internal-secret",
				Handler: handler.New(handler.Services{Validation: validationFake{issues: tc.issues}, Versions: versionFake{version: version}, Cache: freeCache{}, IdempotencyTTL: time.Minute}),
				DB:      upDB{}, Cache: upPinger{},
			}).Handler()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(map[bool]string{true: body, false: ""}[tc.method == http.MethodPost]))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("x-tenant-id", uuid.NewString())
			req.Header.Set("x-user-id", uuid.NewString())
			req.Header.Set("x-tenant-roles", "tenant_admin")
			spec.Request(t, req)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			spec.Response(t, req, w.Code, w.Header(), w.Body.Bytes())
			spec.Undeclared(t, req, w.Code, w.Body.Bytes())
		})
	}
}

func TestPublicContract_PublishAcceptsNoBody(t *testing.T) {
	spec := contract.Load(t, publicSpec)
	gin.SetMode(gin.TestMode)
	n := int32(1)
	at := time.Now().UTC()
	version := &domain.WorkflowVersion{ID: uuid.New(), WorkflowID: uuid.New(), Status: domain.VersionStatusPublished, VersionNumber: &n, PublishedAt: &at, CreatedAt: at, RecordVersion: 2}
	router := httpadapter.NewRouter(httpadapter.RouterConfig{
		GinConfig: gincommon.Config{ServiceName: "definition-contract-test", Domain: "workflow", Environment: "test"}, AppEnv: "test", InternalAPIToken: "internal-secret",
		Handler: handler.New(handler.Services{Versions: versionFake{version: version}, Cache: freeCache{}, IdempotencyTTL: time.Minute}),
		DB:      upDB{}, Cache: upPinger{},
	}).Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+version.WorkflowID.String()+"/versions/"+version.ID.String()+"/publish", nil)
	req.Header.Set("x-tenant-id", uuid.NewString())
	req.Header.Set("x-user-id", uuid.NewString())
	req.Header.Set("x-tenant-roles", "tenant_admin")
	spec.Request(t, req)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	spec.Response(t, req, w.Code, w.Header(), w.Body.Bytes())
	spec.Undeclared(t, req, w.Code, w.Body.Bytes())
}

func TestPublicContract_CompiledPlansMatchTheirSchema(t *testing.T) {
	spec := contract.Load(t, publicSpec)
	gin.SetMode(gin.TestMode)
	files, err := filepath.Glob("testdata/*.bpmn")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	compiled := 0
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			xml, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			collab, err := bpmn_compiler.New().CompileForPublish(context.Background(), string(xml))
			if err != nil {
				t.Skipf("fixture does not compile for publish: %v", err)
			}
			plan, err := json.Marshal(collab)
			if err != nil {
				t.Fatal(err)
			}
			compiled++
			n := int32(1)
			at := time.Now().UTC()
			version := &domain.WorkflowVersion{ID: uuid.New(), WorkflowID: uuid.New(), Status: domain.VersionStatusPublished, VersionNumber: &n, BPMNXML: string(xml), IsValid: true, CreatedAt: at, UpdatedAt: at, PublishedAt: &at, RecordVersion: 2}
			planText := string(plan)
			version.CompiledPlanJSON = &planText
			router := httpadapter.NewRouter(httpadapter.RouterConfig{
				GinConfig: gincommon.Config{ServiceName: "definition-contract-test", Domain: "workflow", Environment: "test"}, AppEnv: "test", InternalAPIToken: "internal-secret",
				Handler: handler.New(handler.Services{Versions: versionFake{version: version}, Cache: freeCache{}, IdempotencyTTL: time.Minute}),
				DB:      upDB{}, Cache: upPinger{},
			}).Handler()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/"+version.WorkflowID.String()+"/versions/"+version.ID.String(), nil)
			req.Header.Set("x-tenant-id", uuid.NewString())
			req.Header.Set("x-user-id", uuid.NewString())
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			spec.Response(t, req, w.Code, w.Header(), w.Body.Bytes())
			spec.Undeclared(t, req, w.Code, w.Body.Bytes())
		})
	}
	if compiled < 8 {
		t.Fatalf("%d fixtures compiled, want at least 8 of %d", compiled, len(files))
	}
}
