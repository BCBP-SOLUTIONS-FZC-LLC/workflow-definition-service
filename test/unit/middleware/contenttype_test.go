package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
)

func contentTypeRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequireJSONContentType())
	r.POST("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func doCT(r *gin.Engine, method, body, contentType string) (int, string) {
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	var req *http.Request
	if rdr != nil {
		req = httptest.NewRequest(method, "/x", rdr)
	} else {
		req = httptest.NewRequest(method, "/x", nil)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

func TestRequireJSONContentType(t *testing.T) {
	r := contentTypeRouter()
	tests := []struct {
		name        string
		method      string
		body        string
		contentType string
		want        int
	}{
		{"json body passes", http.MethodPost, `{"a":1}`, "application/json", http.StatusOK},
		{"json with charset passes", http.MethodPost, `{"a":1}`, "application/json; charset=utf-8", http.StatusOK},
		{"wrong content-type with body -> 415", http.MethodPost, `<xml/>`, "application/xml", http.StatusUnsupportedMediaType},
		{"missing content-type with body -> 415", http.MethodPost, `{"a":1}`, "", http.StatusUnsupportedMediaType},
		{"empty body passes (bodyless POST)", http.MethodPost, "", "", http.StatusOK},
		{"GET passes regardless", http.MethodGet, "", "", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := doCT(r, tt.method, tt.body, tt.contentType)
			if code != tt.want {
				t.Fatalf("got %d, want %d", code, tt.want)
			}
			if tt.want == http.StatusUnsupportedMediaType && !strings.Contains(body, "UNSUPPORTED_MEDIA_TYPE") {
				t.Errorf("expected UNSUPPORTED_MEDIA_TYPE code in body; got %s", body)
			}
		})
	}
}
