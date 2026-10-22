package handler_test

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func newObservedRouter(h *handler.Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Header("X-Trace-ID", "4bf92f3577b34da6a3ce929d0e0e4736")
		c.Header("X-Request-ID", "b1a2c3d4-e5f6-7890-abcd-1234567890ef")
	})
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	registerRoutes(r, h)
	return r
}

func decodeProblem(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var prob map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &prob))
	return prob
}

func TestProblem_IsAProblemDocumentCarryingTheCorrelationIds(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
			return nil, domain.ErrNoDraftExists
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newObservedRouter(h), adminReq(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"/draft", nil))

	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "application/problem+json", strings.Split(w.Header().Get("Content-Type"), ";")[0])
	prob := decodeProblem(t, w)
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", prob["trace_id"])
	assert.Equal(t, "b1a2c3d4-e5f6-7890-abcd-1234567890ef", prob["request_id"])
}

func TestStrictBodies_AnUnknownFieldIsRefusedNamed(t *testing.T) {
	cases := []struct {
		name, method, path string
		body               map[string]any
	}{
		{"draft update", http.MethodPut, "/api/v1/workflows/" + testWFID.String() + "/draft", map[string]any{"name": "x", "record_version": 1, "unknown_member": "typo"}},
		{"workflow create", http.MethodPost, "/api/v1/workflows", map[string]any{"key": "k", "name": "n", "bpmn_xml": "<x/>", "unknown_member": "typo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			h := newHandler(&fakeWorkflowSvc{
				create: func(context.Context, uuid.UUID, uuid.UUID, string, string, string, string) (*domain.Workflow, *domain.WorkflowVersion, error) {
					called = true
					return nil, nil, nil
				},
			}, &fakeDraftSvc{
				update: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
					called = true
					return newDraftVersion(), nil
				},
			}, &fakeVersionSvc{}, &fakeValidationSvc{})

			w := do(newRouter(h), adminReq(tc.method, tc.path, tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code)
			assert.False(t, called, "a refused body must not reach the service")
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			params, _ := prob["invalid_params"].([]any)
			require.Len(t, params, 1)
			param := params[0].(map[string]any)
			assert.Equal(t, "unknown_member", param["name"])
			assert.Equal(t, "is not a known field", param["reason"])
		})
	}
}

func TestUpdateDraft_TheTokenComesFromIfMatchOrTheBody(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		body    map[string]any
		want    int64
		wantNil bool
	}{
		{"body only", "", map[string]any{"name": "x", "record_version": 7}, 7, false},
		{"header only", `"3"`, map[string]any{"name": "x"}, 3, false},
		{"unquoted header", "4", map[string]any{"name": "x"}, 4, false},
		{"header wins over the body", `"3"`, map[string]any{"name": "x", "record_version": 7}, 3, false},
		{"an unparseable header falls back to the body", "garbage", map[string]any{"name": "x", "record_version": 5}, 5, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got int64 = -1
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
				update: func(_ context.Context, _, _, _ uuid.UUID, r service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
					got = r.RecordVersion
					return newDraftVersion(), nil
				},
			}, &fakeVersionSvc{}, &fakeValidationSvc{})
			r := adminReq(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft", tc.body)
			if tc.header != "" {
				r.Header.Set("If-Match", tc.header)
			}

			w := do(newRouter(h), r)

			require.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestUpdateDraft_ARequestWithoutTheTokenNamesIt(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		update: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
			return nil, domain.ErrRecordVersionRequired
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), adminReq(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft", map[string]any{"name": "x"}))

	require.Equal(t, http.StatusBadRequest, w.Code)
	prob := decodeProblem(t, w)
	assert.Equal(t, "BAD_REQUEST", prob["code"])
	params, _ := prob["invalid_params"].([]any)
	require.Len(t, params, 1)
	param := params[0].(map[string]any)
	assert.Equal(t, "record_version", param["name"])
	assert.Equal(t, "is required", param["reason"])
}

func TestUpdateDraft_AStaleSaveAnswersWithTheCurrentVersion(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		update: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
			return nil, &domain.DraftConcurrencyError{Current: 9}
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), adminReq(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft", map[string]any{"name": "x", "record_version": 2}))

	require.Equal(t, http.StatusConflict, w.Code)
	prob := decodeProblem(t, w)
	assert.Equal(t, "DRAFT_CONCURRENCY", prob["code"])
	assert.EqualValues(t, 9, prob["current_record_version"])
}

func TestUpdateDraft_AConflictWithNoKnownVersionOmitsIt(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		update: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
			return nil, domain.ErrDraftConcurrency
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), adminReq(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft", map[string]any{"name": "x", "record_version": 2}))

	require.Equal(t, http.StatusConflict, w.Code)
	assert.NotContains(t, decodeProblem(t, w), "current_record_version")
}

func TestIdempotency_AReplayWithAnotherIfMatchIsAReplayOfADifferentRequest(t *testing.T) {
	store := map[string]string{}
	cache := &fakeCache{
		get: func(_ context.Context, key string) (string, error) {
			if v, ok := store[key]; ok {
				return v, nil
			}
			return "", assert.AnError
		},
		set: func(_ context.Context, key, value string, _ time.Duration) error {
			store[key] = value
			return nil
		},
	}
	runs := 0
	r := newIdempotencyRouter(cache, func(c *gin.Context) {
		runs++
		c.JSON(http.StatusOK, gin.H{"saved": true})
	})
	send := func(ifMatch string) *httptest.ResponseRecorder {
		httpReq := req(http.MethodPost, "/idempotency-test", map[string]any{"name": "x"})
		httpReq.Header.Set("Idempotency-Key", "same-key")
		httpReq.Header.Set("If-Match", ifMatch)
		return do(r, httpReq)
	}

	require.Equal(t, http.StatusOK, send(`"3"`).Code)
	again := send(`"3"`)
	assert.Equal(t, http.StatusOK, again.Code, "the same request replays")
	assert.Equal(t, 1, runs)

	stale := send(`"9"`)
	require.Equal(t, http.StatusConflict, stale.Code, "another If-Match under the same key is not the same request")
	assert.Equal(t, "IDEMPOTENCY_KEY_REPLAY", decodeProblem(t, stale)["code"])
	assert.Equal(t, 1, runs)
}

func TestStrictBodies_TrailingDataAfterTheValueIsRefused(t *testing.T) {
	called := false
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		update: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
			called = true
			return newDraftVersion(), nil
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})
	r := httptest.NewRequest(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft",
		strings.NewReader(`{"name":"x","record_version":1}{"zzz":1}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", testTenantID.String())
	r.Header.Set("x-user-id", testUserID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")

	w := do(newRouter(h), r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.False(t, called, "a body with a second value must not reach the service")
}

func TestUpdateDraft_OnlyAWellFormedIfMatchOverridesTheBody(t *testing.T) {
	for _, header := range []string{`W/"3"`, `"3`, `3"`, `*`, `"1","2"`, "garbage", ""} {
		t.Run("header "+header, func(t *testing.T) {
			var got int64 = -1
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
				update: func(_ context.Context, _, _, _ uuid.UUID, r service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
					got = r.RecordVersion
					return newDraftVersion(), nil
				},
			}, &fakeVersionSvc{}, &fakeValidationSvc{})
			r := adminReq(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft", map[string]any{"name": "x", "record_version": 8})
			if header != "" {
				r.Header.Set("If-Match", header)
			}

			w := do(newRouter(h), r)

			require.Equal(t, http.StatusOK, w.Code)
			assert.EqualValues(t, 8, got, "a header that is not a plain or quoted integer is ignored")
		})
	}
}

func TestStrictBodies_EveryPublicEndpointWithABodyRefusesAnUnknownField(t *testing.T) {
	wf, ver, mod := uuid.NewString(), uuid.NewString(), uuid.NewString()
	paths := map[string]struct{ method, path string }{
		"create workflow":          {http.MethodPost, "/api/v1/workflows"},
		"validate":                 {http.MethodPost, "/api/v1/workflows/validate"},
		"update draft":             {http.MethodPut, "/api/v1/workflows/" + wf + "/draft"},
		"publish version":          {http.MethodPost, "/api/v1/workflows/" + wf + "/versions/" + ver + "/publish"},
		"clone version":            {http.MethodPost, "/api/v1/workflows/" + wf + "/versions/" + ver + "/clone"},
		"write connector secret":   {http.MethodPost, "/api/v1/connectors/credentials"},
		"create module":            {http.MethodPost, "/api/v1/modules"},
		"add module version":       {http.MethodPost, "/api/v1/modules/" + mod + "/versions"},
		"create starter":           {http.MethodPost, "/api/v1/starters"},
		"starter from the version": {http.MethodPost, "/api/v1/starters/from-workflow-version/" + ver},
	}
	h := handler.New(handler.Services{
		Workflows: &fakeWorkflowSvc{}, Drafts: &fakeDraftSvc{}, Versions: &fakeVersionSvc{}, Validation: &fakeValidationSvc{},
		Modules: &fakeModuleSvc{get: func(_ context.Context, tenantID, id uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: id, TenantID: &tenantID, Scope: domain.ScopeTenant}, nil, nil
		}},
		Starters: &fakeStarterSvc{},
	})
	router := newRouter(h)
	for name, tc := range paths {
		t.Run(name, func(t *testing.T) {
			r := adminReq(tc.method, tc.path, map[string]any{"unknown_member": "typo"})
			r.Header.Set("x-tenant-roles", "tenant_admin,platform_operator")
			w := do(router, r)

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			params, _ := decodeProblem(t, w)["invalid_params"].([]any)
			require.Len(t, params, 1, "the refusal must name the unknown field, not fail on something else")
			assert.Equal(t, "unknown_member", params[0].(map[string]any)["name"])
		})
	}
}

func TestStrictBodies_TheInternalAliasWriteRefusesAnUnknownField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/alias", handler.New(handler.Services{}).WriteConnectorRestAlias)
	w := do(r, req(http.MethodPost, "/alias", map[string]any{"alias": "a", "method": "GET", "baseURL": "https://x", "pathTemplate": "/", "unknown_member": 1}))

	require.Equal(t, http.StatusBadRequest, w.Code)
	params, _ := decodeProblem(t, w)["invalid_params"].([]any)
	require.Len(t, params, 1)
}
