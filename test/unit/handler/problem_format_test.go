package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
