package contract_test

import (
	"context"
	"encoding/json"
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
	req.Header.Set("x-tenant-roles", "tenant_member")
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
		})
	}
}
