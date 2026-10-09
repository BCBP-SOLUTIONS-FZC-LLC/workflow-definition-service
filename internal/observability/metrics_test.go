package observability_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		ServiceName:  "workflow-definition-svc",
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
		"workflow_definition_svc_submissions_total",
		"workflow_definition_svc_archive_total",
		"workflow_definition_svc_publish_total",
		"workflow_definition_svc_clone_total",
		"workflow_definition_svc_promote_total",
		"workflow_definition_svc_validation_failures_total",
		"workflow_definition_svc_publish_latency_seconds",
		"workflow_definition_svc_compile_duration_seconds",
		"workflow_definition_svc_cache_hits_total",
		"workflow_definition_svc_cache_misses_total",
		"workflow_definition_svc_internal_events_ingest_total",
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
