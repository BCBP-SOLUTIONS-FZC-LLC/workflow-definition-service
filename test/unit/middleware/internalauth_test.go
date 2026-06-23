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

func TestRequireInternalToken(t *testing.T) {
	tests := []struct {
		name        string
		configToken string
		sendHeader  bool
		headerValue string
		wantStatus  int
	}{
		{"disabled when token empty", "", false, "", http.StatusOK},
		{"matching header passes", "secret", true, "secret", http.StatusOK},
		{"missing header rejected", "secret", false, "", http.StatusUnauthorized},
		{"mismatched header rejected", "secret", true, "wrong", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := internalRouter(tt.configToken)
			if code := doInternalReq(r, tt.headerValue, tt.sendHeader); code != tt.wantStatus {
				t.Errorf("status = %d, want %d", code, tt.wantStatus)
			}
		})
	}
}
