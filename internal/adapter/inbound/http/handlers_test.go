package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	return c, w
}

func TestSwaggerThemeHandler(t *testing.T) {
	c, w := newTestContext()
	SwaggerThemeHandler(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Errorf("content-type = %q, want text/css", ct)
	}
	if w.Body.Len() == 0 {
		t.Error("body must not be empty")
	}
}

func TestSwaggerInitializerHandler(t *testing.T) {
	c, w := newTestContext()
	SwaggerInitializerHandler(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("content-type = %q, want application/javascript", ct)
	}
	// Verify the URL was updated from the gin-swagger default "doc.json".
	if !strings.Contains(w.Body.String(), "/api/openapi.yaml") {
		t.Error("swagger initializer must reference /api/openapi.yaml, not doc.json")
	}
}

// TestAsyncAPIHandler_FileNotFound exercises the error path: when the spec file
// does not exist relative to the test working directory, the handler must return
// 500 rather than panic.
func TestAsyncAPIHandler_FileNotFound(t *testing.T) {
	c, w := newTestContext()
	AsyncAPIHandler(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (spec file not present in test CWD)", w.Code)
	}
}
