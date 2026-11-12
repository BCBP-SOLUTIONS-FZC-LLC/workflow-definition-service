package handler_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"

	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const (
	w12cTypeUnauthorized = "https://api.workflow.platform/errors/unauthorized"
	w12cTypeInternal     = "https://api.workflow.platform/errors/internal-error"
	w12cTypeBadRequest   = "https://api.workflow.platform/errors/bad-request"
	w12cTypeTooLarge     = "https://api.workflow.platform/errors/payload-too-large"
)

func w12cAssertProblem(t *testing.T, w *httptest.ResponseRecorder, status int, wantType, wantCode, wantDetail, wantInstance string) {
	t.Helper()
	require.Equal(t, status, w.Code, w.Body.String())
	assert.Equal(t, "application/problem+json", strings.Split(w.Header().Get("Content-Type"), ";")[0])
	prob := decodeProblem(t, w)
	assert.Equal(t, wantType, prob["type"])
	assert.Equal(t, float64(status), prob["status"])
	assert.Equal(t, wantCode, prob["code"])
	assert.Equal(t, wantDetail, prob["detail"])
	assert.Equal(t, wantInstance, prob["instance"])
}

func w12cIdentityRouter(identity gin.HandlerFunc, reached *bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(identity)
	r.GET("/test", func(c *gin.Context) { *reached = true; c.Status(http.StatusOK) })
	return r
}

func TestIdentityMiddleware_RefusalTypeUriFollowsTheStatus(t *testing.T) {
	cases := []struct {
		name       string
		build      func(reached *bool) *gin.Engine
		tenant     string
		user       string
		wantStatus int
		wantType   string
		wantCode   string
		wantDetail string
	}{
		{
			name: "no request context is an internal error",
			build: func(reached *bool) *gin.Engine {
				gin.SetMode(gin.TestMode)
				r := gin.New()
				r.Use(httpmiddleware.RequireIdentity(nil))
				r.GET("/test", func(c *gin.Context) { *reached = true; c.Status(http.StatusOK) })
				return r
			},
			tenant: testTenantID.String(), user: testUserID.String(),
			wantStatus: http.StatusInternalServerError, wantType: w12cTypeInternal,
			wantCode: "INTERNAL_ERROR", wantDetail: "missing request context",
		},
		{
			name: "tenant that is not a UUID is unauthorized",
			build: func(reached *bool) *gin.Engine {
				return w12cIdentityRouter(httpmiddleware.RequireIdentity(nil), reached)
			},
			tenant: "zzq_unknown", user: testUserID.String(),
			wantStatus: http.StatusUnauthorized, wantType: w12cTypeUnauthorized,
			wantCode: "UNAUTHORIZED", wantDetail: "x-tenant-id must be a UUID",
		},
		{
			name: "user that is not a UUID is unauthorized",
			build: func(reached *bool) *gin.Engine {
				return w12cIdentityRouter(httpmiddleware.RequireIdentity(nil), reached)
			},
			tenant: testTenantID.String(), user: "zzq_unknown",
			wantStatus: http.StatusUnauthorized, wantType: w12cTypeUnauthorized,
			wantCode: "UNAUTHORIZED", wantDetail: "x-user-id must be a UUID",
		},
		{
			name: "blank tenant header is unauthorized",
			build: func(reached *bool) *gin.Engine {
				gin.SetMode(gin.TestMode)
				r := gin.New()
				r.Use(httpmiddleware.RequireIdentityHeaders(nil))
				r.GET("/test", func(c *gin.Context) { *reached = true; c.Status(http.StatusOK) })
				return r
			},
			tenant: " ", user: testUserID.String(),
			wantStatus: http.StatusUnauthorized, wantType: w12cTypeUnauthorized,
			wantCode: "UNAUTHORIZED", wantDetail: "x-tenant-id must be a UUID",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			r := tc.build(&reached)
			httpReq := httptest.NewRequest(http.MethodGet, "/test", nil)
			httpReq.Header.Set("x-tenant-id", tc.tenant)
			httpReq.Header.Set("x-user-id", tc.user)

			w := do(r, httpReq)

			w12cAssertProblem(t, w, tc.wantStatus, tc.wantType, tc.wantCode, tc.wantDetail, "/test")
			assert.False(t, reached, "a refused request must not reach the handler")
		})
	}
}

func TestIdentityMiddleware_AcceptedRequestStoresIdentityForIdentityFrom(t *testing.T) {
	var got httpmiddleware.Identity
	var gotOK bool
	r := w12cIdentityRouter(httpmiddleware.RequireIdentity(nil), new(bool))
	r.GET("/w12c-identity", func(c *gin.Context) { got, gotOK = httpmiddleware.IdentityFrom(c) })

	w := do(r, req(http.MethodGet, "/w12c-identity", nil))

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, gotOK)
	assert.Equal(t, testTenantID, got.TenantID)
	assert.Equal(t, testUserID, got.UserID)
}

type w12cUnreadBody struct{ reads int }

func (b *w12cUnreadBody) Read([]byte) (int, error) {
	b.reads++
	return 0, io.EOF
}

func (b *w12cUnreadBody) Close() error { return nil }

func TestLimitRequestBody_DeclaredLengthOverTheLimitAndBodyNeverRead_HandlerCompletes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(httpmiddleware.LimitRequestBody())
	reached := false
	r.POST("/test", func(c *gin.Context) { reached = true; c.Status(http.StatusOK) })
	body := &w12cUnreadBody{}
	httpReq := httptest.NewRequest(http.MethodPost, "/test", nil)
	httpReq.Body = body
	httpReq.ContentLength = 10<<20 + 1

	w := do(r, httpReq)

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.True(t, reached)
	assert.Zero(t, body.reads, "the limit is applied when the body is read, not from the declared length")
}

func TestLimitRequestBody_DeclaredLengthOverTheLimitAndBodyRead_Refuses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(httpmiddleware.LimitRequestBody())
	var readErr error
	r.POST("/test", func(c *gin.Context) {
		_, readErr = io.Copy(io.Discard, c.Request.Body)
		c.Status(http.StatusOK)
	})
	httpReq := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(strings.Repeat("a", 10<<20+1)))

	do(r, httpReq)

	var tooLarge *http.MaxBytesError
	require.ErrorAs(t, readErr, &tooLarge)
	assert.Equal(t, int64(10<<20), tooLarge.Limit)
}

func TestPublishVersion_BodyOverTenMegabytes_Returns413PayloadTooLargeProblem(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	r.Use(httpmiddleware.LimitRequestBody())
	published := 0
	registerRoutes(r, newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		publish: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, bool) (*domain.WorkflowVersion, error) {
			published++
			return &domain.WorkflowVersion{ID: testVerID}, nil
		},
	}, &fakeValidationSvc{}))
	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	big := `{"force_publish_structural":false,"pad":"` + strings.Repeat("a", 10<<20+512) + `"}`
	httpReq := httptest.NewRequest(http.MethodPost, path, strings.NewReader(big))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-tenant-id", testTenantID.String())
	httpReq.Header.Set("x-user-id", testUserID.String())
	httpReq.Header.Set("x-tenant-roles", "tenant_admin")

	w := do(r, httpReq)

	w12cAssertProblem(t, w, http.StatusRequestEntityTooLarge, w12cTypeTooLarge, "PAYLOAD_TOO_LARGE",
		"request body exceeds the 10 MB limit", path)
	assert.Zero(t, published, "an oversized body never reaches the service")
}

func w12cCreateRouter(created *int) *gin.Engine {
	r := newRouter(newHandler(&fakeWorkflowSvc{
		create: func(context.Context, uuid.UUID, uuid.UUID, string, string, string, string) (*domain.Workflow, *domain.WorkflowVersion, error) {
			*created++
			return newWorkflow(), &domain.WorkflowVersion{ID: testVerID}, nil
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{}))
	return r
}

func TestCreateWorkflow_IdentityHeaderNotAUUID_Returns401UnauthorizedProblem(t *testing.T) {
	cases := []struct {
		name       string
		tenant     string
		user       string
		wantDetail string
	}{
		{"tenant is not a UUID", "zzq_unknown", testUserID.String(), "x-tenant-id must be a UUID"},
		{"user is not a UUID", testTenantID.String(), "zzq_unknown", "x-user-id must be a UUID"},
		{"neither is a UUID, the tenant is judged first", "zzq_unknown", "zzq_other", "x-tenant-id must be a UUID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			created := 0
			r := w12cCreateRouter(&created)
			httpReq := adminReq(http.MethodPost, "/api/v1/workflows", map[string]string{
				"key": "flow-one", "name": "Flow one", "bpmn_xml": "<bpmn/>",
			})
			httpReq.Header.Set("x-tenant-id", tc.tenant)
			httpReq.Header.Set("x-user-id", tc.user)

			w := do(r, httpReq)

			w12cAssertProblem(t, w, http.StatusUnauthorized, w12cTypeUnauthorized, "UNAUTHORIZED", tc.wantDetail, "/api/v1/workflows")
			assert.Zero(t, created, "a refused identity never reaches the service")
		})
	}
}

func TestCreateWorkflow_EmptyBodyWithJSONContentTypeCheck_Returns400NotValidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	created := 0
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	r.Use(httpmiddleware.RequireJSONContentType())
	registerRoutes(r, newHandler(&fakeWorkflowSvc{
		create: func(context.Context, uuid.UUID, uuid.UUID, string, string, string, string) (*domain.Workflow, *domain.WorkflowVersion, error) {
			created++
			return newWorkflow(), &domain.WorkflowVersion{ID: testVerID}, nil
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{}))
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(""))
	httpReq.Header.Set("x-tenant-id", testTenantID.String())
	httpReq.Header.Set("x-user-id", testUserID.String())
	httpReq.Header.Set("x-tenant-roles", "tenant_admin")
	require.Zero(t, httpReq.ContentLength)

	w := do(r, httpReq)

	w12cAssertProblem(t, w, http.StatusBadRequest, w12cTypeBadRequest, "BAD_REQUEST", "request body is not valid JSON", "/api/v1/workflows")
	assert.NotContains(t, decodeProblem(t, w), "invalid_params")
	assert.Zero(t, created, "an empty body never reaches the service")
}
