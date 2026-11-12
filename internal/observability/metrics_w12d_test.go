package observability_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

// w12dFindSeries gathers the shared registry and returns the family with the
// given name and, inside it, the series whose variable labels equal want
// (nil when the family or the series is absent).
func w12dFindSeries(t *testing.T, family string, want map[string]string) (*dto.MetricFamily, *dto.Metric) {
	t.Helper()
	families, err := gincommon.MetricsGatherer().Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() != family {
			continue
		}
		for _, m := range f.GetMetric() {
			got := map[string]string{}
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			match := true
			for k, v := range want {
				if got[k] != v {
					match = false
				}
			}
			if match {
				return f, m
			}
		}
		return f, nil
	}
	return nil, nil
}

func w12dLabelNames(m *dto.Metric) []string {
	names := make([]string, 0, len(m.GetLabel()))
	for _, l := range m.GetLabel() {
		names = append(names, l.GetName())
	}
	return names
}

func w12dConstLabelNames() []string {
	names := make([]string, 0)
	for k := range gincommon.MetricsConstLabels() {
		names = append(names, k)
	}
	return names
}

func w12dBucketBounds(h *dto.Histogram) []float64 {
	bounds := make([]float64, 0, len(h.GetBucket()))
	for _, b := range h.GetBucket() {
		bounds = append(bounds, b.GetUpperBound())
	}
	return bounds
}

type w12dHistogramState struct {
	count   uint64
	sum     float64
	buckets map[float64]uint64
}

func w12dHistogram(t *testing.T, family string, want map[string]string) w12dHistogramState {
	t.Helper()
	state := w12dHistogramState{buckets: map[float64]uint64{}}
	_, m := w12dFindSeries(t, family, want)
	if m == nil {
		return state
	}
	h := m.GetHistogram()
	state.count = h.GetSampleCount()
	state.sum = h.GetSampleSum()
	for _, b := range h.GetBucket() {
		state.buckets[b.GetUpperBound()] = b.GetCumulativeCount()
	}
	return state
}

func w12dUnique(prefix string) string {
	return prefix + "_" + uuid.NewString()
}

func TestRegister_RepeatedAndConcurrentCalls_KeepTheSameCollectorInstances(t *testing.T) {
	initIdentity(t)
	before := map[string]any{
		"submissions":   observability.WFSubmissionsTotal,
		"archive":       observability.WFArchiveTotal,
		"publish":       observability.WFPublishTotal,
		"clone":         observability.WFCloneTotal,
		"promote":       observability.WFPromoteTotal,
		"validation":    observability.WFValidationFailuresTotal,
		"publishLat":    observability.WFPublishLatencySeconds,
		"compile":       observability.WFCompileDurationSeconds,
		"cacheHits":     observability.WFCacheHitsTotal,
		"cacheMisses":   observability.WFCacheMissesTotal,
		"ingest":        observability.InternalEventsIngestTotal,
		"rls":           observability.RLSViolationsTotal,
		"purgeRows":     observability.TenantPurgeRowsDeletedTotal,
		"purgeFailures": observability.TenantPurgeFailuresTotal,
		"dependency":    observability.DependencyRequestSeconds,
	}
	for name, c := range before {
		require.NotNilf(t, c, "collector %s must be created by Register", name)
	}

	var wg sync.WaitGroup
	panics := make(chan any, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panics <- r
				}
			}()
			observability.Register()
		}()
	}
	wg.Wait()
	close(panics)
	for r := range panics {
		t.Fatalf("a concurrent Register call panicked: %v", r)
	}

	after := map[string]any{
		"submissions":   observability.WFSubmissionsTotal,
		"archive":       observability.WFArchiveTotal,
		"publish":       observability.WFPublishTotal,
		"clone":         observability.WFCloneTotal,
		"promote":       observability.WFPromoteTotal,
		"validation":    observability.WFValidationFailuresTotal,
		"publishLat":    observability.WFPublishLatencySeconds,
		"compile":       observability.WFCompileDurationSeconds,
		"cacheHits":     observability.WFCacheHitsTotal,
		"cacheMisses":   observability.WFCacheMissesTotal,
		"ingest":        observability.InternalEventsIngestTotal,
		"rls":           observability.RLSViolationsTotal,
		"purgeRows":     observability.TenantPurgeRowsDeletedTotal,
		"purgeFailures": observability.TenantPurgeFailuresTotal,
		"dependency":    observability.DependencyRequestSeconds,
	}
	for name, c := range before {
		require.Samef(t, c, after[name], "Register must not replace the %s collector", name)
	}
}

func TestAddRLSViolations_CollectorNotCreated_IsANoopThatLeavesTheRegisteredOneUntouched(t *testing.T) {
	initIdentity(t)
	registered := observability.RLSViolationsTotal
	require.NotNil(t, registered)
	violationType := w12dUnique("zzq_rls_nil")
	series := registered.WithLabelValues(violationType)
	before := testutil.ToFloat64(series)
	observability.RLSViolationsTotal = nil
	t.Cleanup(func() { observability.RLSViolationsTotal = registered })

	require.NotPanics(t, func() { observability.AddRLSViolations(violationType, 7) })

	require.Nil(t, observability.RLSViolationsTotal, "the call must not create a collector")
	require.InDelta(t, before, testutil.ToFloat64(series), 0)
}

func TestAddRLSViolations_AccumulatesPerTypeWithItsOwnSeries(t *testing.T) {
	initIdentity(t)
	typeA := w12dUnique("zzq_rls_a")
	typeB := w12dUnique("zzq_rls_b")
	beforeA := testutil.ToFloat64(observability.RLSViolationsTotal.WithLabelValues(typeA))
	beforeB := testutil.ToFloat64(observability.RLSViolationsTotal.WithLabelValues(typeB))

	observability.AddRLSViolations(typeA, 2)
	observability.AddRLSViolations(typeA, 3)
	observability.AddRLSViolations(typeB, 4)

	fam, seriesA := w12dFindSeries(t, "workflow_definition_service_rls_violations_total", map[string]string{"violation_type": typeA})
	require.NotNil(t, fam)
	require.NotNil(t, seriesA)
	require.Equal(t, dto.MetricType_COUNTER, fam.GetType())
	require.ElementsMatch(t, append(w12dConstLabelNames(), "violation_type"), w12dLabelNames(seriesA))
	require.InDelta(t, beforeA+5, seriesA.GetCounter().GetValue(), 0)
	_, seriesB := w12dFindSeries(t, "workflow_definition_service_rls_violations_total", map[string]string{"violation_type": typeB})
	require.NotNil(t, seriesB)
	require.InDelta(t, beforeB+4, seriesB.GetCounter().GetValue(), 0)
}

func TestAddTenantPurgeRows_AccumulatesPerTableAndZeroCreatesTheSeries(t *testing.T) {
	initIdentity(t)
	tableA := w12dUnique("zzq_purge_a")
	tableB := w12dUnique("zzq_purge_b")
	tableZero := w12dUnique("zzq_purge_zero")
	const family = "workflow_definition_service_tenant_purge_rows_deleted_total"
	_, absent := w12dFindSeries(t, family, map[string]string{"table": tableZero})
	require.Nil(t, absent, "the zero-amount table must not exist before the call")

	observability.AddTenantPurgeRows(tableA, 3)
	observability.AddTenantPurgeRows(tableA, 4)
	observability.AddTenantPurgeRows(tableB, 1)
	observability.AddTenantPurgeRows(tableZero, 0)

	fam, seriesA := w12dFindSeries(t, family, map[string]string{"table": tableA})
	require.NotNil(t, fam)
	require.NotNil(t, seriesA)
	require.Equal(t, dto.MetricType_COUNTER, fam.GetType())
	require.ElementsMatch(t, append(w12dConstLabelNames(), "table"), w12dLabelNames(seriesA))
	require.InDelta(t, 7, seriesA.GetCounter().GetValue(), 0)
	_, seriesB := w12dFindSeries(t, family, map[string]string{"table": tableB})
	require.NotNil(t, seriesB)
	require.InDelta(t, 1, seriesB.GetCounter().GetValue(), 0)
	_, seriesZero := w12dFindSeries(t, family, map[string]string{"table": tableZero})
	require.NotNil(t, seriesZero, "a zero amount must still create the series")
	require.InDelta(t, 0, seriesZero.GetCounter().GetValue(), 0)
}

func TestInitTenantPurgeTables_EmptyAndDuplicateInputsCreateTheRightNumberOfSeries(t *testing.T) {
	initIdentity(t)
	counter := observability.TenantPurgeRowsDeletedTotal
	const family = "workflow_definition_service_tenant_purge_rows_deleted_total"

	start := testutil.CollectAndCount(counter)
	observability.InitTenantPurgeTables([]string{})
	observability.InitTenantPurgeTables(nil)
	require.Equal(t, start, testutil.CollectAndCount(counter), "an empty list creates no series")

	dup := w12dUnique("zzq_init_dup")
	observability.InitTenantPurgeTables([]string{dup, dup, dup})
	require.Equal(t, start+1, testutil.CollectAndCount(counter), "a repeated name creates one series")

	one := w12dUnique("zzq_init_one")
	two := w12dUnique("zzq_init_two")
	observability.InitTenantPurgeTables([]string{one, two})
	require.Equal(t, start+3, testutil.CollectAndCount(counter), "each distinct name creates one series")

	for _, table := range []string{dup, one, two} {
		_, m := w12dFindSeries(t, family, map[string]string{"table": table})
		require.NotNilf(t, m, "table %q must be scraped", table)
		require.InDelta(t, 0, m.GetCounter().GetValue(), 0)
	}
}

func TestInitTenantPurgeTables_ExistingCountIsNotReset(t *testing.T) {
	initIdentity(t)
	table := w12dUnique("zzq_init_keep")
	observability.AddTenantPurgeRows(table, 5)

	observability.InitTenantPurgeTables([]string{table})

	require.InDelta(t, 5, testutil.ToFloat64(observability.TenantPurgeRowsDeletedTotal.WithLabelValues(table)), 0)
}

func TestObserveHistogram_RegisteredHistogramsRecordCountSumAndBuckets(t *testing.T) {
	initIdentity(t)
	cases := []struct {
		name     string
		family   string
		observer prometheus.Observer
		other    string
	}{
		{
			name:     "publish latency",
			family:   "workflow_definition_service_publish_latency_seconds",
			observer: observability.WFPublishLatencySeconds,
			other:    "workflow_definition_service_compile_duration_seconds",
		},
		{
			name:     "compile duration",
			family:   "workflow_definition_service_compile_duration_seconds",
			observer: observability.WFCompileDurationSeconds,
			other:    "workflow_definition_service_publish_latency_seconds",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := w12dHistogram(t, tc.family, nil)
			otherBefore := w12dHistogram(t, tc.other, nil)

			observability.ObserveHistogram(tc.observer, 0.25)
			observability.ObserveHistogram(tc.observer, 3)

			after := w12dHistogram(t, tc.family, nil)
			require.Equal(t, before.count+2, after.count)
			require.InDelta(t, before.sum+3.25, after.sum, 1e-9)
			require.Equal(t, before.buckets[0.25]+1, after.buckets[0.25], "0.25 falls in le=0.25")
			require.Equal(t, before.buckets[0.1], after.buckets[0.1], "nothing falls in le=0.1")
			require.Equal(t, before.buckets[2.5]+1, after.buckets[2.5], "3 does not fall in le=2.5")
			require.Equal(t, before.buckets[5]+2, after.buckets[5], "both fall in le=5")
			otherAfter := w12dHistogram(t, tc.other, nil)
			require.Equal(t, otherBefore.count, otherAfter.count, "the other histogram must not move")
		})
	}
}

func TestObserveDependency_RecordsElapsedSecondsPerOutcome(t *testing.T) {
	initIdentity(t)
	const family = "platform_dependency_request_seconds"
	dependency := w12dUnique("zzq_dep")
	success := map[string]string{"dependency": dependency, "operation": "zzq_op", "outcome": "success"}
	failure := map[string]string{"dependency": dependency, "operation": "zzq_op", "outcome": "error"}
	require.Zero(t, w12dHistogram(t, family, success).count)
	require.Zero(t, w12dHistogram(t, family, failure).count)

	observability.ObserveDependency(dependency, "zzq_op", nil, 1500*time.Millisecond)
	observability.ObserveDependency(dependency, "zzq_op", errors.New("boom"), 250*time.Millisecond)
	observability.ObserveDependency(dependency, "zzq_op", fmt.Errorf("call failed: %w", context.Canceled), 2*time.Second)

	ok := w12dHistogram(t, family, success)
	require.Equal(t, uint64(1), ok.count)
	require.InDelta(t, 1.5, ok.sum, 1e-9)
	bad := w12dHistogram(t, family, failure)
	require.Equal(t, uint64(2), bad.count, "a plain error and a wrapped context.Canceled are both errors")
	require.InDelta(t, 2.25, bad.sum, 1e-9)

	fam, m := w12dFindSeries(t, family, success)
	require.NotNil(t, fam)
	require.NotNil(t, m)
	require.Equal(t, dto.MetricType_HISTOGRAM, fam.GetType())
	require.ElementsMatch(t, append(w12dConstLabelNames(), "dependency", "operation", "outcome"), w12dLabelNames(m))
}

func TestRegister_EveryCollectorHasItsDocumentedNameTypeAndLabels(t *testing.T) {
	initIdentity(t)
	const status = "zzq_status"
	const eventType = "zzq_event"
	const result = "zzq_result"
	observability.WFSubmissionsTotal.WithLabelValues(status)
	observability.WFArchiveTotal.WithLabelValues(status)
	observability.WFPublishTotal.WithLabelValues(status)
	observability.WFCloneTotal.WithLabelValues(status)
	observability.WFPromoteTotal.WithLabelValues(status)
	observability.InternalEventsIngestTotal.WithLabelValues(eventType, result)
	observability.RLSViolationsTotal.WithLabelValues("zzq_violation")
	observability.TenantPurgeRowsDeletedTotal.WithLabelValues("zzq_table")
	observability.DependencyRequestSeconds.WithLabelValues("zzq_dependency", "zzq_operation", "success")

	cases := []struct {
		name      string
		typ       dto.MetricType
		variable  []string
		series    map[string]string
		wantBound []float64
	}{
		{name: "workflow_definition_service_submissions_total", typ: dto.MetricType_COUNTER, variable: []string{"status"}, series: map[string]string{"status": status}},
		{name: "workflow_definition_service_archive_total", typ: dto.MetricType_COUNTER, variable: []string{"status"}, series: map[string]string{"status": status}},
		{name: "workflow_definition_service_publish_total", typ: dto.MetricType_COUNTER, variable: []string{"status"}, series: map[string]string{"status": status}},
		{name: "workflow_definition_service_clone_total", typ: dto.MetricType_COUNTER, variable: []string{"status"}, series: map[string]string{"status": status}},
		{name: "workflow_definition_service_promote_total", typ: dto.MetricType_COUNTER, variable: []string{"status"}, series: map[string]string{"status": status}},
		{name: "workflow_definition_service_validation_failures_total", typ: dto.MetricType_COUNTER},
		{name: "workflow_definition_service_cache_hits_total", typ: dto.MetricType_COUNTER},
		{name: "workflow_definition_service_cache_misses_total", typ: dto.MetricType_COUNTER},
		{name: "workflow_definition_service_tenant_purge_failures_total", typ: dto.MetricType_COUNTER},
		{name: "workflow_definition_service_internal_events_ingest_total", typ: dto.MetricType_COUNTER, variable: []string{"event_type", "result"}, series: map[string]string{"event_type": eventType, "result": result}},
		{name: "workflow_definition_service_rls_violations_total", typ: dto.MetricType_COUNTER, variable: []string{"violation_type"}, series: map[string]string{"violation_type": "zzq_violation"}},
		{name: "workflow_definition_service_tenant_purge_rows_deleted_total", typ: dto.MetricType_COUNTER, variable: []string{"table"}, series: map[string]string{"table": "zzq_table"}},
		{name: "workflow_definition_service_publish_latency_seconds", typ: dto.MetricType_HISTOGRAM, wantBound: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}},
		{name: "workflow_definition_service_compile_duration_seconds", typ: dto.MetricType_HISTOGRAM, wantBound: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}},
		{name: "platform_dependency_request_seconds", typ: dto.MetricType_HISTOGRAM, variable: []string{"dependency", "operation", "outcome"}, series: map[string]string{"dependency": "zzq_dependency", "operation": "zzq_operation", "outcome": "success"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fam, m := w12dFindSeries(t, tc.name, tc.series)
			require.NotNilf(t, fam, "family %q must be registered", tc.name)
			require.NotNilf(t, m, "a series of %q must exist", tc.name)
			require.Equal(t, tc.typ, fam.GetType())
			require.ElementsMatch(t, append(w12dConstLabelNames(), tc.variable...), w12dLabelNames(m))
			if tc.wantBound != nil {
				require.Equal(t, tc.wantBound, w12dBucketBounds(m.GetHistogram()))
			}
		})
	}
}

func TestStartSpan_SpanCarriesItsNameAndScopeAndTheCloserEndsIt(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "none")
	t.Setenv("OTEL_TRACES_IGNORE_REMOTE_PARENT_SAMPLED", "false")
	t.Setenv("OTEL_TRACES_SAMPLER_RATIO", "1")
	shutdown, err := gincommon.InitTracingWithConfig(gincommon.TracingConfig{
		ServiceName: "workflow-definition-service", BuildVersion: "test", Domain: observability.Domain, Environment: "test",
	})
	require.NoError(t, err)
	t.Cleanup(shutdown)

	spanName := w12dUnique("zzq_span")
	sampledParent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	ctx, end := observability.StartSpan(trace.ContextWithRemoteSpanContext(context.Background(), sampledParent), spanName)
	span := trace.SpanFromContext(ctx)
	require.True(t, span.SpanContext().IsValid())
	require.True(t, span.IsRecording(), "the span is open until the closer runs")

	named, ok := span.(interface{ Name() string })
	require.True(t, ok, "the started span exposes its name")
	require.Equal(t, spanName, named.Name())

	method := reflect.ValueOf(span).MethodByName("InstrumentationScope")
	require.True(t, method.IsValid(), "the started span exposes its instrumentation scope")
	require.Equal(t, "workflow/workflow-definition-service", method.Call(nil)[0].FieldByName("Name").String())

	ended, ok := span.(interface{ EndTime() time.Time })
	require.True(t, ok, "the started span exposes its end time")
	require.True(t, ended.EndTime().IsZero(), "the span has not ended before the closer runs")

	end()

	require.False(t, span.IsRecording(), "the closer must end the span")
	require.False(t, ended.EndTime().IsZero(), "the closer must stamp the end of the span")
}

func TestIncCounter_RegisteredCounterRisesByOnePerCallAndOthersStay(t *testing.T) {
	initIdentity(t)
	hits := testutil.ToFloat64(observability.WFCacheHitsTotal)
	misses := testutil.ToFloat64(observability.WFCacheMissesTotal)

	observability.IncCounter(observability.WFCacheHitsTotal)
	observability.IncCounter(observability.WFCacheHitsTotal)

	require.InDelta(t, hits+2, testutil.ToFloat64(observability.WFCacheHitsTotal), 0)
	require.InDelta(t, misses, testutil.ToFloat64(observability.WFCacheMissesTotal), 0)
}

func TestIncCounterVec_AddsOnePerCallToTheMatchingLabelSet(t *testing.T) {
	initIdentity(t)
	eventA := w12dUnique("zzq_event_a")
	eventB := w12dUnique("zzq_event_b")
	vec := observability.InternalEventsIngestTotal
	aOK := testutil.ToFloat64(vec.WithLabelValues(eventA, "ok"))
	aFail := testutil.ToFloat64(vec.WithLabelValues(eventA, "zzq_failed"))
	bOK := testutil.ToFloat64(vec.WithLabelValues(eventB, "ok"))

	observability.IncCounterVec(vec, eventA, "ok")
	observability.IncCounterVec(vec, eventA, "ok")
	observability.IncCounterVec(vec, eventB, "ok")

	require.InDelta(t, aOK+2, testutil.ToFloat64(vec.WithLabelValues(eventA, "ok")), 0)
	require.InDelta(t, aFail, testutil.ToFloat64(vec.WithLabelValues(eventA, "zzq_failed")), 0)
	require.InDelta(t, bOK+1, testutil.ToFloat64(vec.WithLabelValues(eventB, "ok")), 0)
}
