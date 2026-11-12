package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
)

func sgDReadyzRouter(db DBPinger, cache Pinger) *Router {
	gin.SetMode(gin.TestMode)
	return NewRouter(RouterConfig{
		GinConfig: testGinConfig,
		AppEnv:    "test",
		Handler:   handler.New(handler.Services{}),
		DB:        db, Cache: cache,
	})
}

func sgDGetReadyz(t *testing.T, r *Router) (int, map[string]any, string) {
	t.Helper()
	w := httptest.NewRecorder()
	r.engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), w.Body.String())
	return w.Code, body, w.Header().Get("Content-Type")
}

func TestReadyz_Healthy_EchoesThePoolStatistics(t *testing.T) {
	t.Parallel()

	r := sgDReadyzRouter(fakeDBPinger{health: DBHealth{Healthy: true, Utilization: 0.25, AcquiredConns: 5, MaxConns: 20}}, fakePinger{})

	code, body, contentType := sgDGetReadyz(t, r)

	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, contentType, "application/json")
	assert.Equal(t, map[string]any{
		"status":         "OK",
		"cache":          "ok",
		"db_utilization": 0.25,
		"db_conns":       float64(5),
		"db_max_conns":   float64(20),
	}, body)
}

func TestReadyz_UnhealthyDatabase_ReportsOnlyStatusAndDBWhateverTheCacheSays(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		cache Pinger
	}{
		{name: "cache answering", cache: fakePinger{}},
		{name: "cache also down", cache: fakePinger{err: assert.AnError}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := sgDReadyzRouter(fakeDBPinger{health: DBHealth{Healthy: false, Utilization: 1, AcquiredConns: 20, MaxConns: 20}}, tc.cache)

			code, body, contentType := sgDGetReadyz(t, r)

			assert.Equal(t, http.StatusServiceUnavailable, code)
			assert.Contains(t, contentType, "application/json")
			assert.NotContains(t, contentType, "problem")
			assert.Equal(t, map[string]any{"status": "unavailable", "db": "unreachable"}, body)
		})
	}
}

func TestReadyz_AfterBeginDrain_ReportsDrainingWithNothingElse(t *testing.T) {
	t.Parallel()

	r := sgDReadyzRouter(fakeDBPinger{health: DBHealth{Healthy: true, Utilization: 0.5, AcquiredConns: 1, MaxConns: 2}}, fakePinger{})
	r.BeginDrain()

	code, body, contentType := sgDGetReadyz(t, r)

	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Contains(t, contentType, "application/json")
	assert.NotContains(t, contentType, "problem")
	assert.Equal(t, map[string]any{"status": "draining"}, body)
}

func TestReadyz_OtherMethods_AreMethodNotAllowedProblems(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		method string
	}{
		{name: "POST", method: http.MethodPost},
		{name: "HEAD", method: http.MethodHead},
		{name: "PUT", method: http.MethodPut},
		{name: "DELETE", method: http.MethodDelete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := sgDReadyzRouter(fakeDBPinger{health: DBHealth{Healthy: true}}, fakePinger{})

			w := httptest.NewRecorder()
			r.engine.ServeHTTP(w, httptest.NewRequest(tc.method, "/readyz", nil))

			require.Equal(t, http.StatusMethodNotAllowed, w.Code)
			assert.Equal(t, "GET", w.Header().Get("Allow"))
			assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
			var prob map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &prob), w.Body.String())
			assert.Equal(t, "METHOD_NOT_ALLOWED", prob["code"])
			assert.Equal(t, float64(http.StatusMethodNotAllowed), prob["status"])
			assert.Equal(t, "/readyz", prob["instance"])
			assert.Equal(t, "https://api.workflow.platform/errors/method-not-allowed", prob["type"])
		})
	}
}
