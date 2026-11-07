package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
)

type readyzDrainCountingDB struct {
	health DBHealth
	calls  atomic.Int32
}

func (f *readyzDrainCountingDB) Health(context.Context) DBHealth {
	f.calls.Add(1)
	return f.health
}

func TestReadyz_DrainingWithUnhealthyDatabase_ReportsDrainingWithoutCheckingDatabase(t *testing.T) {
	cases := []struct {
		name   string
		health DBHealth
	}{
		{name: "database unhealthy", health: DBHealth{Healthy: false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gin.SetMode(gin.TestMode)
			db := &readyzDrainCountingDB{health: tc.health}
			router := NewRouter(RouterConfig{
				GinConfig: testGinConfig,
				AppEnv:    "test",
				Handler:   handler.New(handler.Services{}),
				DB:        db, Cache: fakePinger{},
			})

			w := httptest.NewRecorder()
			router.engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			require.Equal(t, http.StatusServiceUnavailable, w.Code)
			var before map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &before))
			require.Equal(t, "unavailable", before["status"], "without a drain the unhealthy database is what is reported")
			callsBeforeDrain := db.calls.Load()
			require.Equal(t, int32(1), callsBeforeDrain)

			router.BeginDrain()

			w = httptest.NewRecorder()
			router.engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			assert.Equal(t, http.StatusServiceUnavailable, w.Code)
			var after map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &after))
			assert.Equal(t, "draining", after["status"])
			assert.NotContains(t, after, "db")
			assert.Equal(t, callsBeforeDrain, db.calls.Load(), "the drain answer must not touch the database")
		})
	}
}
