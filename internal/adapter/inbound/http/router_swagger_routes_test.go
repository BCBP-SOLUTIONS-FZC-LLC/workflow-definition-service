package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
)

func newSwaggerRoutesDevEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return NewRouter(RouterConfig{
		GinConfig: testGinConfig,
		AppEnv:    "dev",
		Handler:   handler.New(handler.Services{}),
		DB:        fakeDBPinger{}, Cache: fakePinger{},
	}).engine
}

func TestSwaggerRoute_DevMode_OverridesMatchBySuffix(t *testing.T) {
	t.Parallel()
	engine := newSwaggerRoutesDevEngine()

	cases := []struct {
		name        string
		path        string
		contentType string
		body        []byte
	}{
		{"theme at root", "/swagger/index.css", "text/css; charset=utf-8", swaggerThemeCSS},
		{"theme under a nested path", "/swagger/nested/deeper/index.css", "text/css; charset=utf-8", swaggerThemeCSS},
		{"initializer at root", "/swagger/swagger-initializer.js", "application/javascript; charset=utf-8", swaggerInitializerJS},
		{"initializer under a nested path", "/swagger/nested/swagger-initializer.js", "application/javascript; charset=utf-8", swaggerInitializerJS},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))

			require.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, tc.contentType, w.Header().Get("Content-Type"))
			assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
			assert.Equal(t, tc.body, w.Body.Bytes(), "the embedded override must be served, not the stock asset")
		})
	}
}

func TestSwaggerRoute_DevMode_StockHandlerServesBundledAsset(t *testing.T) {
	t.Parallel()
	engine := newSwaggerRoutesDevEngine()

	cases := []struct {
		name        string
		path        string
		contentType string
	}{
		{"ui bundle", "/swagger/swagger-ui-bundle.js", "application/javascript"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))

			require.Equal(t, http.StatusOK, w.Code)
			assert.True(t, strings.HasPrefix(w.Header().Get("Content-Type"), tc.contentType),
				"got %q", w.Header().Get("Content-Type"))
			assert.NotEmpty(t, w.Body.Bytes())
			assert.NotEqual(t, swaggerInitializerJS, w.Body.Bytes())
		})
	}
}

func TestSwaggerRoute_DevMode_UnknownFile_Returns404PlainText(t *testing.T) {
	t.Parallel()
	engine := newSwaggerRoutesDevEngine()

	cases := []struct {
		name string
		path string
	}{
		{"no file name", "/swagger/"},
		{"unknown file", "/swagger/unknown.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))

			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Equal(t, "Not Found", w.Body.String())
			assert.NotContains(t, w.Header().Get("Content-Type"), "problem+json")
		})
	}
}
