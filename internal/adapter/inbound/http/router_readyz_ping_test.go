package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

type loggedLine struct {
	msg    string
	fields map[string]any
}

type readyzLog struct {
	mu    sync.Mutex
	warns []loggedLine
}

func (l *readyzLog) Debug(string, map[string]any) {}
func (l *readyzLog) Info(string, map[string]any)  {}
func (l *readyzLog) Error(string, map[string]any) {}
func (l *readyzLog) Fatal(string, map[string]any) {}
func (l *readyzLog) Warn(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, loggedLine{msg, fields})
}

func TestReadyz_FailedCachePing_IsLoggedCountedAndTheAnswerStays200Degraded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	log := &readyzLog{}
	r := NewRouter(RouterConfig{
		GinConfig: testGinConfig,
		AppEnv:    "test",
		Log:       log,
		Handler:   handler.New(handler.Services{}),
		DB:        fakeDBPinger{health: DBHealth{Healthy: true}},
		Cache:     fakePinger{err: errors.New("valkey: connection refused")},
	})
	observability.Register()
	before := testutil.ToFloat64(observability.ReadinessCachePingFailuresTotal)

	w := httptest.NewRecorder()
	r.engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"cache":"degraded"`)
	require.Len(t, log.warns, 1)
	assert.Equal(t, "valkey: connection refused", log.warns[0].fields["error"])
	assert.Equal(t, before+1, testutil.ToFloat64(observability.ReadinessCachePingFailuresTotal))
}

func TestReadyz_HealthyCachePing_LogsNothingAndCountsNothing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	log := &readyzLog{}
	r := NewRouter(RouterConfig{
		GinConfig: testGinConfig,
		AppEnv:    "test",
		Log:       log,
		Handler:   handler.New(handler.Services{}),
		DB:        fakeDBPinger{health: DBHealth{Healthy: true}},
		Cache:     fakePinger{},
	})
	observability.Register()
	before := testutil.ToFloat64(observability.ReadinessCachePingFailuresTotal)

	w := httptest.NewRecorder()
	r.engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	assert.Contains(t, w.Body.String(), `"cache":"ok"`)
	assert.Empty(t, log.warns)
	assert.Equal(t, before, testutil.ToFloat64(observability.ReadinessCachePingFailuresTotal))
}

func TestReadyz_FailedCachePingWithNoLoggerWired_StillAnswers200Degraded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := NewRouter(RouterConfig{
		GinConfig: testGinConfig,
		AppEnv:    "test",
		Handler:   handler.New(handler.Services{}),
		DB:        fakeDBPinger{health: DBHealth{Healthy: true}},
		Cache:     fakePinger{err: errors.New("valkey down")},
	})

	w := httptest.NewRecorder()
	r.engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	assert.Equal(t, http.StatusOK, w.Code, "a missing logger must never turn a cache outage into a failed readiness probe")
	assert.Contains(t, w.Body.String(), `"cache":"degraded"`)
}
