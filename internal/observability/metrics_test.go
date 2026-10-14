package observability_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/logger"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/obsregistry"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

// testPool returns a *pgxpool.Pool that satisfies pgmetrics.PoolStatProvider
// without dialing a database — pgxpool.New only parses the DSN and builds the
// pool's internal puddle.Pool; connections are acquired lazily, so Stat()
// works (reporting all-zero counts) without ever reaching Postgres. A
// zero-value &pgxpool.Stat{} instead panics: its embedded *puddle.Stat is nil.
func initIdentity(t *testing.T) {
	t.Helper()
	log, err := logger.NewLogger("dev")
	require.NoError(t, err)
	_ = gincommon.ObservabilityMiddlewares(gincommon.Config{
		Logger:       log,
		ServiceName:  "workflow-definition-service",
		BuildVersion: "test",
		Domain:       observability.Domain,
		Environment:  "test",
	})
	observability.Register()
}

func scrape(t *testing.T) string {
	t.Helper()
	initIdentity(t)

	srv := httptest.NewServer(gincommon.MetricsHandler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL) //nolint:noctx,gosec // test-only, fixed httptest URL
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

func TestRegister_EveryMetricIsScrapable(t *testing.T) {
	initIdentity(t)

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

	body := scrape(t)

	metricNames := []string{
		"workflow_definition_service_submissions_total",
		"workflow_definition_service_archive_total",
		"workflow_definition_service_publish_total",
		"workflow_definition_service_clone_total",
		"workflow_definition_service_promote_total",
		"workflow_definition_service_validation_failures_total",
		"workflow_definition_service_publish_latency_seconds",
		"workflow_definition_service_compile_duration_seconds",
		"workflow_definition_service_cache_hits_total",
		"workflow_definition_service_cache_misses_total",
		"workflow_definition_service_internal_events_ingest_total",
	}

	for _, name := range metricNames {
		require.Truef(t, strings.Contains(body, name), "expected metric %q in scrape output", name)
	}
}

func TestRegister_FollowsObservabilityRegistry(t *testing.T) {
	initIdentity(t)
	observability.IncCounterVec(observability.WFPublishTotal, "ok")

	rep, err := obsregistry.Validate(gincommon.MetricsGatherer(), obsregistry.Options{})
	require.NoError(t, err)
	require.Truef(t, rep.OK(), "registry findings: %+v", rep.Findings)
}

func TestRegister_IsIdempotent(t *testing.T) {
	initIdentity(t)
	require.NotPanics(t, func() {
		observability.Register()
		observability.Register()
	})
}

func TestOutcomeLabel(t *testing.T) {
	require.Equal(t, "ok", observability.OutcomeLabel(nil))
	require.Equal(t, "err", observability.OutcomeLabel(errors.New("boom")))
}

// TestNilSafeHelpers_Noop covers the nil branch of IncCounter/IncCounterVec/
// ObserveHistogram directly — the exact shape a call site sees before
// Register has run (e.g. most unit tests in internal/core/service and
// internal/adapter never call it), independent of this package's global
// registration state.
func TestNilSafeHelpers_Noop(t *testing.T) {
	require.NotPanics(t, func() {
		observability.IncCounter(nil)
		observability.IncCounterVec(nil, "a", "b")
		observability.ObserveHistogram(nil, 1.23)
	})
}

func TestStartSpan_ChildSpanSharesTheTrace(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "none")
	shutdown, err := gincommon.InitTracingWithConfig(gincommon.TracingConfig{
		ServiceName: "workflow-definition-service", BuildVersion: "test", Domain: observability.Domain, Environment: "test",
	})
	require.NoError(t, err)
	t.Cleanup(shutdown)

	parent, parentSpan := gincommon.NewTracer("test").Start(context.Background(), "parent")
	defer parentSpan.End()

	child, end := observability.StartSpan(parent, "BPMN_Compilation")
	defer end()

	require.NotEmpty(t, gincommon.SpanTraceID(child))
	require.Equal(t, gincommon.SpanTraceID(parent), gincommon.SpanTraceID(child))
	require.NotEqual(t, parent, child, "the child span must replace the span carried by the context")
}

func TestAddRLSViolations_RecordsByTypeAndIsNilSafe(t *testing.T) {
	observability.AddRLSViolations("cross_tenant_access", 1)

	initIdentity(t)
	cross := observability.RLSViolationsTotal.WithLabelValues("cross_tenant_access")
	before := testutil.ToFloat64(cross)
	observability.AddRLSViolations("cross_tenant_access", 3)

	require.InDelta(t, before+3, testutil.ToFloat64(cross), 0)
}

func TestObserveDependency_RecordsOutcomeByDependencyAndOperation(t *testing.T) {
	require.NotPanics(t, func() { observability.ObserveDependency("execution", "x", nil, 0) })

	initIdentity(t)
	observability.ObserveDependency("execution", "check_active_instances", nil, 20*time.Millisecond)
	observability.ObserveDependency("execution", "check_active_instances", errors.New("boom"), 20*time.Millisecond)

	body := scrape(t)
	require.Contains(t, body, `platform_dependency_request_seconds_count{dependency="execution",domain="workflow"`)
	require.Contains(t, body, `operation="check_active_instances",outcome="success"`)
	require.Contains(t, body, `operation="check_active_instances",outcome="error"`)
}
