package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

type w6bHealthyDB struct{}

func (w6bHealthyDB) Health(context.Context) httpadapter.DBHealth {
	return httpadapter.DBHealth{Healthy: true}
}

type w6bHealthyCache struct{}

func (w6bHealthyCache) Ping(context.Context) error { return nil }

func TestInternalEvents_BodyOverTheLimit_Returns413PayloadTooLargeAndCountsBadPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	log, err := logger.NewLogger("dev")
	require.NoError(t, err)
	router := httpadapter.NewRouter(httpadapter.RouterConfig{
		GinConfig: gincommon.Config{
			Logger: log, ServiceName: "workflow-definition-service", BuildVersion: "test",
			Domain: observability.Domain, Environment: "test",
		},
		AppEnv:  "test",
		Handler: handler.New(handler.Services{}),
		DB:      w6bHealthyDB{},
		Cache:   w6bHealthyCache{},
	})
	observability.Register()
	counter := observability.InternalEventsIngestTotal.WithLabelValues("unknown", "bad_payload")
	before := testutil.ToFloat64(counter)

	body := `{"id":"x","type":"Unhandled","data":{"pad":"` + strings.Repeat("a", 11<<20) + `"}}`
	r := httptest.NewRequest(http.MethodPost, w6bEventsPath, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-user-id", "iam-system")
	r.Header.Set("x-tenant-roles", "iam-system")
	w := httptest.NewRecorder()
	router.Handler().ServeHTTP(w, r)

	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	p := w6bDecodeProblem(t, w)
	assert.Equal(t, "PAYLOAD_TOO_LARGE", p.Code)
	assert.Equal(t, "https://api.workflow.platform/errors/payload-too-large", p.Type)
	assert.Equal(t, "request body exceeds the 10 MB limit", p.Detail)
	assert.Equal(t, w6bEventsPath, p.Instance)
	assert.Equal(t, before+1, testutil.ToFloat64(counter), "the refusal is counted once under the unknown event type")
}
