package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const (
	w8cTypeBadRequest = "https://api.workflow.platform/errors/bad-request"
	w8cTypeTooLarge   = "https://api.workflow.platform/errors/payload-too-large"
	w8cTypeForbidden  = "https://api.workflow.platform/errors/forbidden"
	w8cTypeInternal   = "https://api.workflow.platform/errors/internal-error"
)

func w8cRawReq(method, path, body, roles string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", testTenantID.String())
	r.Header.Set("x-user-id", testUserID.String())
	if roles != "" {
		r.Header.Set("x-tenant-roles", roles)
	}
	return r
}

func w8cAssertProblem(t *testing.T, w *httptest.ResponseRecorder, status int, wantType, wantCode, wantDetail, wantInstance string) map[string]any {
	t.Helper()
	require.Equal(t, status, w.Code, w.Body.String())
	assert.Equal(t, "application/problem+json", strings.Split(w.Header().Get("Content-Type"), ";")[0])
	prob := decodeProblem(t, w)
	assert.Equal(t, wantType, prob["type"])
	assert.Equal(t, float64(status), prob["status"])
	assert.Equal(t, wantCode, prob["code"])
	assert.Equal(t, wantDetail, prob["detail"])
	assert.Equal(t, wantInstance, prob["instance"])
	return prob
}

type w8cModuleCreateSpy struct {
	calls int
	fake  *fakeModuleSvc
}

func w8cNewModuleCreateSpy() *w8cModuleCreateSpy {
	spy := &w8cModuleCreateSpy{}
	spy.fake = &fakeModuleSvc{
		create: func(context.Context, uuid.UUID, uuid.UUID, service.CreateModuleReq) (*domain.Module, *domain.ModuleVersion, error) {
			spy.calls++
			return nil, nil, nil
		},
	}
	return spy
}

func TestCreateModule_BodyIsNotUsableJSON_Returns400BadRequestProblemNamingNoField(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantDetail string
		wantParams []map[string]any
	}{
		{"empty body", ``, "request body is not valid JSON", nil},
		{"whitespace only", " \n\t ", "request body is not valid JSON", nil},
		{"truncated JSON", `{"name": `, "request body is not valid JSON", nil},
		{"text that is not JSON", `zzq_not_json`, "request body is not valid JSON", nil},
		{"data after the JSON value", `{"name":"m","bpmn_xml":"<bpmn/>"} {}`, "request body is not valid JSON", nil},
		{
			"a field of the wrong type",
			`{"name":123,"bpmn_xml":"<bpmn/>"}`,
			"request body has a field of the wrong type",
			[]map[string]any{{"name": "name", "reason": "has the wrong type"}},
		},
		{
			"a scope that is a number",
			`{"name":"m","scope":1,"bpmn_xml":"<bpmn/>"}`,
			"request body has a field of the wrong type",
			[]map[string]any{{"name": "scope", "reason": "has the wrong type"}},
		},
		{
			"a bpmn_xml that is an object",
			`{"name":"m","bpmn_xml":{"a":1}}`,
			"request body has a field of the wrong type",
			[]map[string]any{{"name": "bpmn_xml", "reason": "has the wrong type"}},
		},
		{
			"a description of the wrong type",
			`{"name":"m","description":["x"],"bpmn_xml":"<bpmn/>"}`,
			"request body has a field of the wrong type",
			[]map[string]any{{"name": "description", "reason": "has the wrong type"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := w8cNewModuleCreateSpy()
			log := &fakeLogger{}
			h := handler.New(handler.Services{Modules: spy.fake, Log: log})

			w := do(newRouter(h), w8cRawReq(http.MethodPost, "/api/v1/modules", tc.body, "tenant_admin"))

			prob := w8cAssertProblem(t, w, http.StatusBadRequest, w8cTypeBadRequest, "BAD_REQUEST", tc.wantDetail, "/api/v1/modules")
			if tc.wantParams == nil {
				assert.NotContains(t, prob, "invalid_params")
			} else {
				params, _ := prob["invalid_params"].([]any)
				require.Len(t, params, len(tc.wantParams))
				for i, want := range tc.wantParams {
					got := params[i].(map[string]any)
					assert.Equal(t, want["name"], got["name"])
					assert.Equal(t, want["reason"], got["reason"])
				}
			}
			assert.Zero(t, spy.calls, "a refused body never reaches the service")
			assert.Len(t, log.warnCalls, 1, "the raw bind error is logged, not returned")
			assert.Equal(t, "invalid request body", log.warnCalls[0])
		})
	}
}

func TestCreateModule_BodyOverTenMegabytes_Returns413PayloadTooLargeProblem(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	r.Use(httpmiddleware.LimitRequestBody())
	spy := w8cNewModuleCreateSpy()
	registerRoutes(r, newModuleHandler(spy.fake))
	big := `{"name":"m","bpmn_xml":"` + strings.Repeat("a", 10<<20+512) + `"}`

	w := do(r, w8cRawReq(http.MethodPost, "/api/v1/modules", big, "tenant_admin"))

	prob := w8cAssertProblem(t, w, http.StatusRequestEntityTooLarge, w8cTypeTooLarge, "PAYLOAD_TOO_LARGE",
		"request body exceeds the 10 MB limit", "/api/v1/modules")
	assert.NotContains(t, prob, "invalid_params")
	assert.Zero(t, spy.calls, "an oversized body never reaches the service")
}

func w8cDirectMountRouter(method, path string, fn gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	r.Handle(method, path, fn)
	return r
}

func TestAddModuleVersion_CallerWhoseOnlyRoleIsPlatformOperator_Returns403ForbiddenProblem(t *testing.T) {
	cases := []struct {
		name  string
		roles string
	}{
		{"platform operator alone", "platform_operator"},
		{"platform operator and a non-admin role", "platform_operator,zzq_member"},
	}
	mounts := []struct {
		name  string
		build func(h *handler.Handler) *gin.Engine
	}{
		{"through the route table", newRouter},
		{"handler mounted without the route guard", func(h *handler.Handler) *gin.Engine {
			return w8cDirectMountRouter(http.MethodPost, "/api/v1/modules/:id/versions", h.AddModuleVersion)
		}},
	}
	for _, mount := range mounts {
		for _, tc := range cases {
			t.Run(mount.name+"/"+tc.name, func(t *testing.T) {
				getCalls, addCalls := 0, 0
				h := newModuleHandler(&fakeModuleSvc{
					get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
						getCalls++
						return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
					},
					addVersion: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.AddModuleVersionReq) (*domain.ModuleVersion, error) {
						addCalls++
						return nil, nil
					},
				})
				path := "/api/v1/modules/" + testWFID.String() + "/versions"

				w := do(mount.build(h), w8cRawReq(http.MethodPost, path, `{"bpmn_xml":"<bpmn/>"}`, tc.roles))

				w8cAssertProblem(t, w, http.StatusForbidden, w8cTypeForbidden, "FORBIDDEN", "caller lacks tenant_admin/tenant_owner role", path)
				assert.Zero(t, getCalls, "the role check comes before the module lookup")
				assert.Zero(t, addCalls)
			})
		}
	}
}
