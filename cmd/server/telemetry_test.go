package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/logger"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/obsregistry"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

func TestStandard_LibrariesShareRegistryCollectors(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "none")
	log, err := logger.NewLogger("test")
	require.NoError(t, err)

	cfg := &config.Config{AppEnv: "test", OTELServiceName: "workflow-definition-service"}
	ginCfg := ginConfig(cfg, log)
	shutdown, err := initTelemetry(ginCfg, log)
	require.NoError(t, err)
	t.Cleanup(shutdown)

	require.NotNil(t, observability.WFPublishTotal, "Register must build the service collectors once the identity is set")

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	for _, mw := range gincommon.ObservabilityMiddlewares(ginCfg) {
		engine.Use(mw)
	}
	engine.GET("/healthz", gincommon.HealthHandler())
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	exerciseEveryCollector()

	gathered, err := gincommon.MetricsGatherer().Gather()
	require.NoError(t, err)
	names := map[string]bool{}
	for _, mf := range gathered {
		names[mf.GetName()] = true
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "version" && strings.HasPrefix(mf.GetName(), "platform_") {
					assert.Equal(t, "platform_build_info", mf.GetName(), "version label only on platform_build_info")
				}
			}
		}
	}
	for _, want := range []string{
		"platform_build_info",
		"platform_library_info",
		"platform_http_requests_total",
		"platform_http_request_duration_seconds",
		"workflow_definition_service_publish_total",
		"workflow_definition_service_internal_events_ingest_total",
		"workflow_definition_service_rls_violations_total",
	} {
		assert.True(t, names[want], "%s must be exported", want)
	}

	rep, err := obsregistry.Validate(gincommon.MetricsGatherer(), obsregistry.Options{})
	require.NoError(t, err)
	assert.Empty(t, rep.Errors(), "scrape must conform to the Platform Observability Registry")

	if out := os.Getenv("METRICS_SCRAPE_OUT"); out != "" {
		scrape := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
		req.Header.Set("Accept", "text/plain")
		gincommon.MetricsHandler().ServeHTTP(scrape, req)
		require.NoError(t, os.WriteFile(out, scrape.Body.Bytes(), 0o600))
	}
}

func exerciseEveryCollector() {
	observability.IncCounterVec(observability.WFSubmissionsTotal, "ok")
	observability.IncCounterVec(observability.WFArchiveTotal, "ok")
	observability.IncCounterVec(observability.WFPublishTotal, "ok")
	observability.IncCounterVec(observability.WFCloneTotal, "ok")
	observability.IncCounterVec(observability.WFPromoteTotal, "ok")
	observability.IncCounter(observability.WFValidationFailuresTotal)
	observability.ObserveHistogram(observability.WFPublishLatencySeconds, 0.1)
	observability.ObserveHistogram(observability.WFCompileDurationSeconds, 0.1)
	observability.IncCounter(observability.WFCacheHitsTotal)
	observability.IncCounter(observability.WFCacheMissesTotal)
	observability.IncCounterVec(observability.InternalEventsIngestTotal, "DepartmentMembershipRevoked", "ok")
	observability.AddRLSViolations("cross_tenant_access", 1)
}
