// Package observability centralizes this service's Prometheus metrics —
// previously three separate promauto call sites in internal/core/service,
// internal/adapter/inbound/grpc, and internal/adapter/inbound/http/handler —
// plus the platform-pgcommon pool-stats wiring that used to live directly in
// cmd/server/wire.go. See .claude/operations.md § Prometheus Metrics for the
// full metric table.
package observability

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgmetrics"
)

// publishBuckets is shared by wf_publish_latency_seconds and
// wf_compile_duration_seconds — both measure sub-5s BPMN/DB work at
// comparable granularity.
var publishBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

// Metric vars are nil until Register runs, matching execution_service's own
// internal/observability convention (itself mirroring iam-user-profile's
// internal/adapter/outbound/metrics): callers outside this package must
// nil-guard before use if they might run before Register, since Register is
// invoked once at real process boot (cmd/server's composition root), not on
// package init.
var (
	WFSubmissionsTotal *prometheus.CounterVec
	WFArchiveTotal     *prometheus.CounterVec
	WFPublishTotal     *prometheus.CounterVec
	WFCloneTotal       *prometheus.CounterVec
	WFPromoteTotal     *prometheus.CounterVec

	WFValidationFailuresTotal prometheus.Counter

	WFPublishLatencySeconds  prometheus.Histogram
	WFCompileDurationSeconds prometheus.Histogram

	WFCacheHitsTotal   prometheus.Counter
	WFCacheMissesTotal prometheus.Counter

	InternalEventsIngestTotal *prometheus.CounterVec
)

var registerOnce sync.Once

// Register constructs and registers every metric above, plus this service's
// platform-pgcommon connection-pool gauges, against
// gincommon.MetricsRegisterer() — not prometheus.MustRegister on the default
// registry directly, so custom collectors stay consistent with any test that
// swaps in an isolated registry. Safe to call more than once (e.g. from more
// than one test in this package) — only the first call has any effect.
//
// pool backs the pgcommon_pool_* gauges (pgmetrics.NewPoolStatsCollector);
// serviceName/buildVersion become the {service, version} const labels shared
// by both this package's metrics and platform-pgcommon's own.
func Register(serviceName, buildVersion string, pool pgmetrics.PoolStatProvider) {
	registerOnce.Do(func() {
		register(serviceName, buildVersion, pool)
	})
}

func register(serviceName, buildVersion string, pool pgmetrics.PoolStatProvider) {
	reg := gincommon.MetricsRegisterer()

	WFSubmissionsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "wf_submissions_total",
		Help: "Total workflow creation attempts, labelled by outcome.",
	}, []string{"status"})

	WFArchiveTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "wf_archive_total",
		Help: "Total workflow archive attempts, labelled by outcome.",
	}, []string{"status"})

	WFPublishTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "wf_publish_total",
		Help: "Total version publish attempts, labelled by outcome.",
	}, []string{"status"})

	WFCloneTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "wf_clone_total",
		Help: "Total version clone attempts, labelled by outcome.",
	}, []string{"status"})

	WFPromoteTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "wf_promote_total",
		Help: "Total version promote attempts, labelled by outcome.",
	}, []string{"status"})

	WFValidationFailuresTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "wf_validation_failures_total",
		Help: "Total BPMN validation calls that returned at least one validation error.",
	})

	WFPublishLatencySeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "wf_publish_latency_seconds",
		Help:    "End-to-end latency of PublishVersion (compile + DB tx).",
		Buckets: publishBuckets,
	})

	WFCompileDurationSeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "wf_compile_duration_seconds",
		Help:    "Duration of BPMN compiler.Compile calls (DSL generation from validated BPMN XML).",
		Buckets: publishBuckets,
	})

	WFCacheHitsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "wf_cache_hits_total",
		Help: "Total compiled-plan cache hits on GetCompiledWorkflow.",
	})

	WFCacheMissesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "wf_cache_misses_total",
		Help: "Total compiled-plan cache misses on GetCompiledWorkflow.",
	})

	InternalEventsIngestTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "internal_events_ingest_total",
		Help: "Total internal event ingest calls, labelled by event_type and result.",
	}, []string{"event_type", "result"})

	reg.MustRegister(
		WFSubmissionsTotal,
		WFArchiveTotal,
		WFPublishTotal,
		WFCloneTotal,
		WFPromoteTotal,
		WFValidationFailuresTotal,
		WFPublishLatencySeconds,
		WFCompileDurationSeconds,
		WFCacheHitsTotal,
		WFCacheMissesTotal,
		InternalEventsIngestTotal,
	)

	pgmetrics.InitWithRegisterer(serviceName, buildVersion, reg)
	reg.MustRegister(pgmetrics.NewPoolStatsCollector(pool, serviceName))
}

// OutcomeLabel returns the shared {status} label value for every wf_*_total
// outcome counter above: "ok" when err is nil, "err" otherwise.
func OutcomeLabel(err error) string {
	if err == nil {
		return "ok"
	}
	return "err"
}

// IncCounter is a nil-safe Counter.Inc — every metric var above is nil until
// Register runs (real process boot only; most unit tests never call it), so
// call sites go through this instead of risking a nil-pointer panic.
func IncCounter(c prometheus.Counter) {
	if c != nil {
		c.Inc()
	}
}

// IncCounterVec is IncCounter's *CounterVec counterpart.
func IncCounterVec(cv *prometheus.CounterVec, labelValues ...string) {
	if cv != nil {
		cv.WithLabelValues(labelValues...).Inc()
	}
}

// ObserveHistogram is a nil-safe Histogram.Observe.
func ObserveHistogram(h prometheus.Histogram, seconds float64) {
	if h != nil {
		h.Observe(seconds)
	}
}
