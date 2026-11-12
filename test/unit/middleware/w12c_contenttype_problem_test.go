package middleware_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
)

const w12cTypeUnsupportedMedia = "https://api.workflow.platform/errors/unsupported-media-type"

func w12cRouter(reached *bool, pre ...gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range pre {
		r.Use(mw)
	}
	r.Use(middleware.RequireJSONContentType())
	r.POST("/x", func(c *gin.Context) { *reached = true; c.Status(http.StatusOK) })
	return r
}

func w12cDecode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var p map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p), w.Body.String())
	return p
}

func w12cAssertUnsupportedMedia(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, http.StatusUnsupportedMediaType, w.Code, w.Body.String())
	assert.Equal(t, "application/problem+json", strings.Split(w.Header().Get("Content-Type"), ";")[0])
	p := w12cDecode(t, w)
	assert.Equal(t, w12cTypeUnsupportedMedia, p["type"])
	assert.Equal(t, float64(http.StatusUnsupportedMediaType), p["status"])
	assert.Equal(t, "UNSUPPORTED_MEDIA_TYPE", p["code"])
	assert.Equal(t, "Content-Type must be application/json", p["detail"])
	assert.Equal(t, "/x", p["instance"])
}

func TestRequireJSONContentType_ParameterAfterASpaceBeforeTheSemicolon_Passes(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
	}{
		{"space before the semicolon and a bare parameter", "application/json ;x=1"},
		{"space before the semicolon and a charset", "application/json ; charset=utf-8"},
		{"upper case type with a trailing space", "Application/JSON "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			r := w12cRouter(&reached)
			httpReq := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"a":1}`))
			httpReq.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, httpReq)

			assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.True(t, reached, "the next handler runs")
		})
	}
}

func TestRequireJSONContentType_NearMissTypesWithABody_Return415Problem(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
	}{
		{"longer subtype", "application/jsonx"},
		{"longer subtype with a parameter", "application/jsonx; charset=utf-8"},
		{"json plus problem suffix", "application/json+problem"},
		{"problem plus json", "application/problem+json"},
		{"text json", "text/json"},
		{"missing header", ""},
		{"parameter only", "; charset=utf-8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			r := w12cRouter(&reached)
			httpReq := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"a":1}`))
			if tc.contentType != "" {
				httpReq.Header.Set("Content-Type", tc.contentType)
			}
			w := httptest.NewRecorder()

			r.ServeHTTP(w, httpReq)

			w12cAssertUnsupportedMedia(t, w)
			assert.False(t, reached, "a refused request must not reach the handler")
		})
	}
}

func TestRequireJSONContentType_ChunkedBodyOfUnknownLength_IsCheckedLikeASizedBody(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		wantStatus  int
	}{
		{"text plain is refused", "text/plain", http.StatusUnsupportedMediaType},
		{"missing type is refused", "", http.StatusUnsupportedMediaType},
		{"json passes", "application/json", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			r := w12cRouter(&reached)
			httpReq := httptest.NewRequest(http.MethodPost, "/x", nil)
			httpReq.Body = io.NopCloser(strings.NewReader(`{"a":1}`))
			httpReq.ContentLength = -1
			if tc.contentType != "" {
				httpReq.Header.Set("Content-Type", tc.contentType)
			}
			w := httptest.NewRecorder()

			r.ServeHTTP(w, httpReq)

			if tc.wantStatus == http.StatusOK {
				assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
				assert.True(t, reached)
				return
			}
			w12cAssertUnsupportedMedia(t, w)
			assert.False(t, reached, "a refused request must not reach the handler")
		})
	}
}

func TestMiddlewareProblem_InstanceIsThePathWithoutTheQueryString(t *testing.T) {
	const secret = "zzq_query_secret"
	cases := []struct {
		name  string
		build func() *gin.Engine
		req   func() *http.Request
	}{
		{
			name: "content type refusal",
			build: func() *gin.Engine {
				reached := false
				return w12cRouter(&reached)
			},
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodPost, "/x?token="+secret+"&a=1", strings.NewReader("hello"))
				r.Header.Set("Content-Type", "text/plain")
				return r
			},
		},
		{
			name: "identity header refusal",
			build: func() *gin.Engine {
				gin.SetMode(gin.TestMode)
				r := gin.New()
				r.Use(middleware.RequireIdentityHeaders(nil))
				r.POST("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
				return r
			},
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/x?token="+secret+"&a=1", nil)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()

			tc.build().ServeHTTP(w, tc.req())

			require.GreaterOrEqual(t, w.Code, http.StatusBadRequest, w.Body.String())
			assert.Equal(t, "/x", w12cDecode(t, w)["instance"])
			assert.NotContains(t, w.Body.String(), secret)
		})
	}
}

func TestMiddlewareProblem_CorrelationIdsComeFromTheResponseHeadersAndAreOmittedWhenUnset(t *testing.T) {
	cases := []struct {
		name          string
		responseHdrs  map[string]string
		requestHdrs   map[string]string
		wantTraceID   string
		wantRequestID string
	}{
		{
			name:         "both response headers set",
			responseHdrs: map[string]string{"X-Trace-ID": "zzq-trace-1", "X-Request-ID": "zzq-request-1"},
			wantTraceID:  "zzq-trace-1", wantRequestID: "zzq-request-1",
		},
		{
			name:         "only the trace header set",
			responseHdrs: map[string]string{"X-Trace-ID": "zzq-trace-2"},
			wantTraceID:  "zzq-trace-2",
		},
		{
			name:          "only the request header set",
			responseHdrs:  map[string]string{"X-Request-ID": "zzq-request-3"},
			wantRequestID: "zzq-request-3",
		},
		{
			name: "neither set",
		},
		{
			name:        "ids sent only on the request are not copied",
			requestHdrs: map[string]string{"X-Trace-ID": "zzq-from-client", "X-Request-ID": "zzq-from-client"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			r := w12cRouter(&reached, func(c *gin.Context) {
				for k, v := range tc.responseHdrs {
					c.Header(k, v)
				}
			})
			httpReq := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("hello"))
			httpReq.Header.Set("Content-Type", "text/plain")
			for k, v := range tc.requestHdrs {
				httpReq.Header.Set(k, v)
			}
			w := httptest.NewRecorder()

			r.ServeHTTP(w, httpReq)

			w12cAssertUnsupportedMedia(t, w)
			p := w12cDecode(t, w)
			if tc.wantTraceID == "" {
				assert.NotContains(t, p, "trace_id")
			} else {
				assert.Equal(t, tc.wantTraceID, p["trace_id"])
			}
			if tc.wantRequestID == "" {
				assert.NotContains(t, p, "request_id")
			} else {
				assert.Equal(t, tc.wantRequestID, p["request_id"])
			}
		})
	}
}
