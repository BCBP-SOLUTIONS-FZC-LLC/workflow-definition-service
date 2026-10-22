package observability

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
)

const (
	Domain       = "workflow"
	serviceScope = "workflow-definition-service"
)

var publishBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

var (
	WFSubmissionsTotal *prometheus.CounterVec
	WFArchiveTotal     *prometheus.CounterVec
	WFPublishTotal     *prometheus.CounterVec
	WFCloneTotal       *prometheus.CounterVec
	WFPromoteTotal     *prometheus.CounterVec

	WFValidationFailuresTotal prometheus.Counter

	WFPublishLatencySeconds  prometheus.Observer
	WFCompileDurationSeconds prometheus.Observer

	WFCacheHitsTotal   prometheus.Counter
	WFCacheMissesTotal prometheus.Counter

	InternalEventsIngestTotal *prometheus.CounterVec

	RLSViolationsTotal *prometheus.CounterVec

	TenantPurgeRowsDeletedTotal *prometheus.CounterVec
	TenantPurgeFailuresTotal    prometheus.Counter

	DependencyRequestSeconds *prometheus.HistogramVec
)

var registerOnce sync.Once

func Register() {
	registerOnce.Do(registerServiceMetrics)
}

func must[C any](c C, err error) C {
	if err != nil {
		panic("metrics: register service collector: " + err.Error())
	}
	return c
}

func registerServiceMetrics() {
	DependencyRequestSeconds = must(gincommon.NewSharedHistogramVec(nil, "platform_dependency_request_seconds"))
	WFSubmissionsTotal = must(gincommon.NewCounterVec(nil,
		"workflow_definition_service_submissions_total",
		"Total workflow creation attempts, labelled by outcome.",
		"status"))
	WFArchiveTotal = must(gincommon.NewCounterVec(nil,
		"workflow_definition_service_archive_total",
		"Total workflow archive attempts, labelled by outcome.",
		"status"))
	WFPublishTotal = must(gincommon.NewCounterVec(nil,
		"workflow_definition_service_publish_total",
		"Total version publish attempts, labelled by outcome.",
		"status"))
	WFCloneTotal = must(gincommon.NewCounterVec(nil,
		"workflow_definition_service_clone_total",
		"Total version clone attempts, labelled by outcome.",
		"status"))
	WFPromoteTotal = must(gincommon.NewCounterVec(nil,
		"workflow_definition_service_promote_total",
		"Total version promote attempts, labelled by outcome.",
		"status"))
	WFValidationFailuresTotal = must(gincommon.NewCounter(nil,
		"workflow_definition_service_validation_failures_total",
		"Total BPMN validation calls that returned at least one validation error."))
	WFPublishLatencySeconds = must(gincommon.NewHistogramVec(nil,
		"workflow_definition_service_publish_latency_seconds",
		"End-to-end latency of PublishVersion (compile + DB tx).",
		publishBuckets)).WithLabelValues()
	WFCompileDurationSeconds = must(gincommon.NewHistogramVec(nil,
		"workflow_definition_service_compile_duration_seconds",
		"Duration of BPMN compiler.Compile calls (DSL generation from validated BPMN XML).",
		publishBuckets)).WithLabelValues()
	WFCacheHitsTotal = must(gincommon.NewCounter(nil,
		"workflow_definition_service_cache_hits_total",
		"Total compiled-plan cache hits on the internal compiled-plan read."))
	WFCacheMissesTotal = must(gincommon.NewCounter(nil,
		"workflow_definition_service_cache_misses_total",
		"Total compiled-plan cache misses on the internal compiled-plan read."))
	InternalEventsIngestTotal = must(gincommon.NewCounterVec(nil,
		"workflow_definition_service_internal_events_ingest_total",
		"Total internal event ingest calls, labelled by event_type and result.",
		"event_type", "result"))
	RLSViolationsTotal = must(gincommon.NewCounterVec(nil,
		"workflow_definition_service_rls_violations_total",
		"Sampled row-level-security violations recorded in rls_violation_log, by violation_type. Each replica counts the rows it sees, so alert on increase() above zero, not on a sum across replicas.",
		"violation_type"))
	TenantPurgeRowsDeletedTotal = must(gincommon.NewCounterVec(nil,
		"workflow_definition_service_tenant_purge_rows_deleted_total",
		"Rows the tenant data purge deleted, by table (workflow, workflow_module or workflow_template).",
		"table"))
	TenantPurgeFailuresTotal = must(gincommon.NewCounter(nil,
		"workflow_definition_service_tenant_purge_failures_total",
		"Tenant purges that failed part-way and will be retried on the next sweep."))
}

func AddRLSViolations(violationType string, n float64) {
	if RLSViolationsTotal != nil {
		RLSViolationsTotal.WithLabelValues(violationType).Add(n)
	}
}

func AddTenantPurgeRows(table string, n int64) {
	if TenantPurgeRowsDeletedTotal != nil {
		TenantPurgeRowsDeletedTotal.WithLabelValues(table).Add(float64(n))
	}
}

func ObserveDependency(dependency, operation string, err error, elapsed time.Duration) {
	if DependencyRequestSeconds == nil {
		return
	}
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	DependencyRequestSeconds.WithLabelValues(dependency, operation, outcome).Observe(elapsed.Seconds())
}

func OutcomeLabel(err error) string {
	if err == nil {
		return "ok"
	}
	return "err"
}

func IncCounter(c prometheus.Counter) {
	if c != nil {
		c.Inc()
	}
}

func IncCounterVec(cv *prometheus.CounterVec, labelValues ...string) {
	if cv != nil {
		cv.WithLabelValues(labelValues...).Inc()
	}
}

func ObserveHistogram(h prometheus.Observer, seconds float64) {
	if h != nil {
		h.Observe(seconds)
	}
}

func StartSpan(ctx context.Context, name string) (context.Context, func()) {
	ctx, span := gincommon.NewTracer(Domain+"/"+serviceScope).Start(ctx, name)
	return ctx, func() { span.End() }
}
