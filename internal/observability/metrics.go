package observability

import (
	"fmt"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/obsregistry"
)

const Domain = "workflow"

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
)

var registerOnce sync.Once

func Register() {
	registerOnce.Do(register)
}

func register() {
	WFSubmissionsTotal = counterVec("submissions_total", "Total workflow creation attempts, labelled by outcome.", "status")
	WFArchiveTotal = counterVec("archive_total", "Total workflow archive attempts, labelled by outcome.", "status")
	WFPublishTotal = counterVec("publish_total", "Total version publish attempts, labelled by outcome.", "status")
	WFCloneTotal = counterVec("clone_total", "Total version clone attempts, labelled by outcome.", "status")
	WFPromoteTotal = counterVec("promote_total", "Total version promote attempts, labelled by outcome.", "status")
	WFValidationFailuresTotal = counter("validation_failures_total", "Total BPMN validation calls that returned at least one validation error.")
	WFPublishLatencySeconds = histogram("publish_latency_seconds", "End-to-end latency of PublishVersion (compile + DB tx).")
	WFCompileDurationSeconds = histogram("compile_duration_seconds", "Duration of BPMN compiler.Compile calls (DSL generation from validated BPMN XML).")
	WFCacheHitsTotal = counter("cache_hits_total", "Total compiled-plan cache hits on the internal compiled-plan read.")
	WFCacheMissesTotal = counter("cache_misses_total", "Total compiled-plan cache misses on the internal compiled-plan read.")
	InternalEventsIngestTotal = counterVec("internal_events_ingest_total", "Total internal event ingest calls, labelled by event_type and result.", "event_type", "result")
}

func metricName(suffix string) string {
	return obsregistry.ServicePrefix(Domain, gincommon.MetricsConstLabels()["service"]) + suffix
}

func counterVec(suffix, help string, labels ...string) *prometheus.CounterVec {
	c, err := gincommon.NewCounterVec(nil, metricName(suffix), help, labels...)
	if err != nil {
		panic(fmt.Sprintf("register %s: %v", suffix, err))
	}
	return c
}

func counter(suffix, help string) prometheus.Counter {
	c, err := gincommon.NewCounter(nil, metricName(suffix), help)
	if err != nil {
		panic(fmt.Sprintf("register %s: %v", suffix, err))
	}
	return c
}

func histogram(suffix, help string) prometheus.Observer {
	h, err := gincommon.NewHistogramVec(nil, metricName(suffix), help, publishBuckets)
	if err != nil {
		panic(fmt.Sprintf("register %s: %v", suffix, err))
	}
	return h.WithLabelValues()
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
