package handler_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"

	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
)

func newMiddlewareRouter(mw ...gin.HandlerFunc) *gin.Engine {
	r := gin.New()
	for _, m := range mw {
		r.Use(m)
	}
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.POST("/test", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func TestRequireIdentity_WithRequestContext_CallsNext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	httpReq := httpReqWithCtx(http.MethodGet, "/test")
	w := do(r, httpReq)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestLimitRequestBody_SmallBody_PassThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := newMiddlewareRouter(httpmiddleware.LimitRequestBody())

	httpReq := req(http.MethodPost, "/test", nil)
	w := do(r, httpReq)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestLimitRequestBody_LargeBody_BodyWrapped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(httpmiddleware.LimitRequestBody())
	r.POST("/test", func(c *gin.Context) {
		if c.Request.Body == nil {
			c.Status(http.StatusOK)
			return
		}
		c.Status(http.StatusOK)
	})

	largeBody := strings.NewReader(strings.Repeat("x", 100))
	httpReq := req(http.MethodPost, "/test", nil)
	httpReq.Body = http.NoBody
	httpReq2, _ := http.NewRequest(http.MethodPost, "/test", largeBody)
	w := do(r, httpReq2)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestLimitRequestBody_ExceedsLimit_Returns413(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	r.Use(httpmiddleware.LimitRequestBody())
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	r.POST("/workflows", h.CreateWorkflow)

	bigJSON := `{"bpmn_xml":"` + strings.Repeat("a", 10<<20+512) + `"}`
	httpReq, _ := http.NewRequest(http.MethodPost, "/workflows", strings.NewReader(bigJSON))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-tenant-id", testTenantID.String())
	httpReq.Header.Set("x-user-id", testUserID.String())
	httpReq.Header.Set("x-tenant-roles", "tenant_admin")

	w := do(r, httpReq)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "PAYLOAD_TOO_LARGE", resp["code"])
}

func TestRequireIdentity_MissingContext_FailsClosedAndLogs(t *testing.T) {
	logger := &fakeLogger{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(httpmiddleware.RequireIdentity(logger))
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := do(r, httpReqWithCtx(http.MethodGet, "/test"))

	assert.Equal(t, http.StatusInternalServerError, w.Code, "a route without the identity chain must not serve")
	require.Len(t, logger.errorCalls, 1)
	assert.Contains(t, logger.errorCalls[0], "RequireIdentity")
}

func TestRequireIdentity_StoresTheParsedIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	var got httpmiddleware.Identity
	r.GET("/test", func(c *gin.Context) {
		got, _ = httpmiddleware.IdentityFrom(c)
		c.Status(http.StatusOK)
	})

	httpReq := httpReqWithCtx(http.MethodGet, "/test")
	httpReq.Header.Set("x-tenant-roles", "tenant_admin")
	w := do(r, httpReq)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, testTenantID, got.TenantID)
	assert.Equal(t, testUserID, got.UserID)
	assert.Equal(t, []string{"tenant_admin"}, got.Roles)
}

func TestRequireIdentityHeaders_AbsentOrBlankIsRefusedInTheServiceFormat(t *testing.T) {
	cases := []struct {
		name       string
		tenant     *string
		user       *string
		wantStatus int
		wantDetail string
	}{
		{"tenant header blank", strPtr(" "), strPtr(testUserID.String()), http.StatusUnauthorized, "x-tenant-id must be a UUID"},
		{"tenant header absent", nil, strPtr(testUserID.String()), http.StatusUnauthorized, "x-tenant-id must be a UUID"},
		{"user header blank", strPtr(testTenantID.String()), strPtr(""), http.StatusUnauthorized, "x-user-id must be a UUID"},
		{"user header absent", strPtr(testTenantID.String()), nil, http.StatusUnauthorized, "x-user-id must be a UUID"},
		{"both present reaches the handler", strPtr(testTenantID.String()), strPtr(testUserID.String()), http.StatusOK, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			logger := &fakeLogger{}
			reached := false
			r := gin.New()
			r.Use(httpmiddleware.RequireIdentityHeaders(logger))
			r.GET("/test", func(c *gin.Context) { reached = true; c.Status(http.StatusOK) })

			httpReq, _ := http.NewRequest(http.MethodGet, "/test", nil)
			if tc.tenant != nil {
				httpReq.Header.Set("x-tenant-id", *tc.tenant)
			}
			if tc.user != nil {
				httpReq.Header.Set("x-user-id", *tc.user)
			}
			w := do(r, httpReq)

			assert.Equal(t, tc.wantStatus, w.Code)
			if tc.wantStatus == http.StatusOK {
				assert.True(t, reached)
				assert.Empty(t, logger.warnCalls)
				return
			}
			assert.False(t, reached, "a refused request must not reach the handler")
			assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, "UNAUTHORIZED", body["code"])
			assert.Equal(t, tc.wantDetail, body["detail"])
			assert.Equal(t, []string{"auth: request rejected"}, logger.warnCalls)
		})
	}
}

func strPtr(s string) *string { return &s }

func httpReqWithCtx(method, path string) *http.Request {
	r, _ := http.NewRequest(method, path, nil)
	r.Header.Set("x-tenant-id", testTenantID.String())
	r.Header.Set("x-user-id", testUserID.String())
	return r
}
