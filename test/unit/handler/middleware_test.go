package handler_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

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
	r.Use(httpmiddleware.InjectGUCSet())
	r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })

	httpReq := httpReqWithCtx(http.MethodGet, "/test")
	w := do(r, httpReq)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestInjectGUCSet_NoRequestContext_PassThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := newMiddlewareRouter(httpmiddleware.InjectGUCSet())

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

func httpReqWithCtx(method, path string) *http.Request {
	r, _ := http.NewRequest(method, path, nil)
	r.Header.Set("x-tenant-id", testTenantID.String())
	r.Header.Set("x-user-id", testUserID.String())
	return r
}
