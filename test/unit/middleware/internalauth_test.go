package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
)

func internalRouter(token string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequireInternalToken(token))
	r.POST("/internal/events", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func doInternalReq(r *gin.Engine, headerValue string, setHeader bool) int {
	req := httptest.NewRequest(http.MethodPost, "/internal/events", nil)
	if setHeader {
		req.Header.Set(middleware.InternalTokenHeader, headerValue)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// Empty token disables the check (local/dev) — every request passes through.
func TestRequireInternalToken_DisabledWhenEmpty(t *testing.T) {
	r := internalRouter("")
	if code := doInternalReq(r, "", false); code != http.StatusOK {
		t.Fatalf("expected 200 when token unset, got %d", code)
	}
}

func TestRequireInternalToken_MatchingHeaderPasses(t *testing.T) {
	r := internalRouter("secret")
	if code := doInternalReq(r, "secret", true); code != http.StatusOK {
		t.Fatalf("expected 200 on matching token, got %d", code)
	}
}

func TestRequireInternalToken_MissingHeaderRejected(t *testing.T) {
	r := internalRouter("secret")
	if code := doInternalReq(r, "", false); code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when header absent, got %d", code)
	}
}

func TestRequireInternalToken_MismatchRejected(t *testing.T) {
	r := internalRouter("secret")
	if code := doInternalReq(r, "wrong", true); code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on token mismatch, got %d", code)
	}
}
