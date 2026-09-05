package observability_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

// testPool returns a *pgxpool.Pool that satisfies pgmetrics.PoolStatProvider
// without dialing a database — pgxpool.New only parses the DSN and builds the
// pool's internal puddle.Pool; connections are acquired lazily, so Stat()
// works (reporting all-zero counts) without ever reaching Postgres. A
// zero-value &pgxpool.Stat{} instead panics: its embedded *puddle.Stat is nil.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://user:pass@localhost:1/db")
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// scrape registers metrics and returns the default registry's /metrics body.
func scrape(t *testing.T) string {
	t.Helper()

	observability.Register("workflow-definition-service", "test", testPool(t))

	srv := httptest.NewServer(promhttp.Handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL) //nolint:noctx,gosec // test-only, fixed httptest URL
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

// TestRegister_EveryMetricIsScrapable records one representative sample on
// every metric, then confirms each appears in a scrape. A *Vec collector
// with an open-ended label and zero recorded observations emits nothing at
// all (not even HELP/TYPE) until something calls WithLabelValues — that's
// normal client_golang behavior, not something Register should work around
// — so this test proves scrapability the same way real call sites will
// eventually trigger it, rather than asserting on incidental zero-value
// output.
func TestRegister_EveryMetricIsScrapable(t *testing.T) {
	observability.Register("workflow-definition-service", "test", testPool(t))

	// Exercised through the same nil-safe helpers real call sites use, not
	// the raw vars, so this also covers IncCounter/IncCounterVec/
	// ObserveHistogram's non-nil branch.
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
		"wf_submissions_total",
		"wf_archive_total",
		"wf_publish_total",
		"wf_clone_total",
		"wf_promote_total",
		"wf_validation_failures_total",
		"wf_publish_latency_seconds",
		"wf_compile_duration_seconds",
		"wf_cache_hits_total",
		"wf_cache_misses_total",
		"internal_events_ingest_total",
		// platform-pgcommon's own pool-stats collector, folded into Register.
		// pgcommon_query_total et al. are CounterVecs with open-ended labels
		// (operation, status) — like our own *Vec metrics, they emit nothing
		// until something calls WithLabelValues, which no code path does here.
		"pgcommon_pool_total_conns",
	}

	for _, name := range metricNames {
		require.Truef(t, strings.Contains(body, name), "expected metric %q in scrape output", name)
	}
}

func TestRegister_IsIdempotent(t *testing.T) {
	pool := testPool(t)
	require.NotPanics(t, func() {
		observability.Register("workflow-definition-service", "test", pool)
		observability.Register("workflow-definition-service", "test", pool)
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
