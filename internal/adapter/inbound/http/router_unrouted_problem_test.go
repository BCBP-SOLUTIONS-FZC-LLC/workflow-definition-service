package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
)

type unroutedProblemBody struct {
	Type      string `json:"type"`
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Instance  string `json:"instance"`
	TraceID   string `json:"trace_id"`
	RequestID string `json:"request_id"`
}

func newUnroutedEngine(appEnv string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	return NewRouter(RouterConfig{
		GinConfig: testGinConfig,
		AppEnv:    appEnv,
		Handler:   handler.New(handler.Services{}),
		DB:        fakeDBPinger{health: DBHealth{Healthy: true}},
		Cache:     fakePinger{},
	}).engine
}

func decodeUnroutedProblem(t *testing.T, w *httptest.ResponseRecorder) unroutedProblemBody {
	t.Helper()
	require.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
	var body unroutedProblemBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body
}

func TestRouter_UnroutedRequest_ReturnsNotFoundProblem(t *testing.T) {
	cases := []struct {
		name         string
		method       string
		target       string
		contentType  string
		wantInstance string
	}{
		{
			name:         "unknown api path without identity headers",
			method:       http.MethodGet,
			target:       "/api/v1/no-such-path",
			wantInstance: "/api/v1/no-such-path",
		},
		{
			name:         "unknown internal path without the system caller identity",
			method:       http.MethodGet,
			target:       "/api/v1/internal/no-such-path",
			wantInstance: "/api/v1/internal/no-such-path",
		},
		{
			name:         "query string is not part of the instance",
			method:       http.MethodGet,
			target:       "/api/v1/no-such-path?page=2&q=abc",
			wantInstance: "/api/v1/no-such-path",
		},
		{
			name:         "unknown api path with a non-JSON content type and no identity",
			method:       http.MethodPost,
			target:       "/api/v1/no-such-path",
			contentType:  "text/plain",
			wantInstance: "/api/v1/no-such-path",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine := newUnroutedEngine("test")
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader("plain body"))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, req)

			require.Equal(t, http.StatusNotFound, w.Code)
			body := decodeUnroutedProblem(t, w)
			assert.Equal(t, "https://api.workflow.platform/errors/not-found", body.Type)
			assert.Equal(t, http.StatusNotFound, body.Status)
			assert.Equal(t, "NOT_FOUND", body.Code)
			assert.Equal(t, tc.wantInstance, body.Instance)
		})
	}
}

func TestRouter_UnroutedRequest_ProblemCarriesTraceAndRequestIDHeaders(t *testing.T) {
	engine := newUnroutedEngine("test")
	w := httptest.NewRecorder()

	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/no-such-path", nil))

	require.Equal(t, http.StatusNotFound, w.Code)
	body := decodeUnroutedProblem(t, w)
	assert.Equal(t, "NOT_FOUND", body.Code)
	traceHeader := w.Header().Get("X-Trace-ID")
	requestHeader := w.Header().Get("X-Request-ID")
	require.NotEmpty(t, traceHeader, "the observability middleware sets X-Trace-ID")
	require.NotEmpty(t, requestHeader, "the observability middleware sets X-Request-ID")
	assert.Equal(t, traceHeader, body.TraceID)
	assert.Equal(t, requestHeader, body.RequestID)
}

func TestRouter_DevOnlyRoutesOutsideDev_ReturnNotFoundProblem(t *testing.T) {
	cases := []struct {
		name   string
		target string
	}{
		{name: "swagger ui", target: "/swagger/x"},
		{name: "asyncapi page", target: "/asyncapi"},
		{name: "public openapi file", target: "/api/openapi.yaml"},
		{name: "internal openapi file", target: "/api/internal-openapi.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine := newUnroutedEngine("production")
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.target, nil))

			require.Equal(t, http.StatusNotFound, w.Code)
			body := decodeUnroutedProblem(t, w)
			assert.Equal(t, "NOT_FOUND", body.Code)
			assert.Equal(t, tc.target, body.Instance)
		})
	}
}
