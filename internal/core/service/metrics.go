package service

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	wfSubmissionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wf_submissions_total",
		Help: "Total workflow creation attempts, labelled by outcome.",
	}, []string{"status"})

	wfArchiveTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wf_archive_total",
		Help: "Total workflow archive attempts, labelled by outcome.",
	}, []string{"status"})

	wfPublishTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wf_publish_total",
		Help: "Total version publish attempts, labelled by outcome.",
	}, []string{"status"})

	wfCloneTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wf_clone_total",
		Help: "Total version clone attempts, labelled by outcome.",
	}, []string{"status"})

	wfPromoteTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wf_promote_total",
		Help: "Total version promote attempts, labelled by outcome.",
	}, []string{"status"})

	wfValidationFailuresTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "wf_validation_failures_total",
		Help: "Total BPMN validation calls that returned at least one validation error.",
	})

	wfPublishLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "wf_publish_latency_seconds",
		Help:    "End-to-end latency of PublishVersion (compile + DB tx).",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	})
)

func outcomeLabel(err error) string {
	if err == nil {
		return "ok"
	}
	return "err"
}
