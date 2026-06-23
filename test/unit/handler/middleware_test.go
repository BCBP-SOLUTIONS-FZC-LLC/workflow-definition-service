package handler_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"

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

func TestInjectGUCSet_WithRequestContext_CallsNext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.InjectGUCSet(nil))
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	httpReq := httpReqWithCtx(http.MethodGet, "/test")
	w := do(r, httpReq)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestInjectGUCSet_NoRequestContext_PassThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := newMiddlewareRouter(httpmiddleware.InjectGUCSet(nil))

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
	r.Use(httpmiddleware.LimitRequestBody())
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	r.POST("/workflows", h.CreateWorkflow)

	// Valid JSON with a string value larger than the 10 MB limit.
	// The JSON decoder reads past the limit mid-string and surfaces *http.MaxBytesError,
	// which errResponse maps to 413 PAYLOAD_TOO_LARGE.
	bigJSON := `{"bpmn_xml":"` + strings.Repeat("a", 10<<20+512) + `"}`
	httpReq, _ := http.NewRequest(http.MethodPost, "/workflows", strings.NewReader(bigJSON))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-tenant-id", testTenantID.String())
	httpReq.Header.Set("x-user-id", testUserID.String())

	w := do(r, httpReq)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "PAYLOAD_TOO_LARGE", resp["code"])
}

func TestInjectGUCSet_MissingContext_LogsWarning(t *testing.T) {
	// Router without ProtectedMiddlewares — RequestContext is absent.
	// InjectGUCSet must log a warn (when logger is non-nil) and still call Next.
	logger := &fakeLogger{}
	gin.SetMode(gin.TestMode)
	r := gin.New() // no ProtectedMiddlewares
	r.Use(httpmiddleware.InjectGUCSet(logger))
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	httpReq := httpReqWithCtx(http.MethodGet, "/test")
	w := do(r, httpReq)

	assert.Equal(t, http.StatusOK, w.Code)
	require.Len(t, logger.warnCalls, 1, "expected one warn log when RequestContext is absent")
	assert.Contains(t, logger.warnCalls[0], "InjectGUCSet")
}

func httpReqWithCtx(method, path string) *http.Request {
	r, _ := http.NewRequest(method, path, nil)
	r.Header.Set("x-tenant-id", testTenantID.String())
	r.Header.Set("x-user-id", testUserID.String())
	return r
}
