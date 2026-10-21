package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel/trace"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/logger"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

func dependencySeries(t *testing.T, dependency, operation, outcome string) uint64 {
	t.Helper()
	families, err := gincommon.MetricsGatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	label := func(m *dto.Metric, name string) string {
		for _, l := range m.GetLabel() {
			if l.GetName() == name {
				return l.GetValue()
			}
		}
		return ""
	}
	for _, f := range families {
		if f.GetName() != "platform_dependency_request_seconds" {
			continue
		}
		for _, m := range f.GetMetric() {
			if label(m, "dependency") == dependency && label(m, "operation") == operation && label(m, "outcome") == outcome {
				return m.GetHistogram().GetSampleCount()
			}
		}
	}
	return 0
}

func registerMetrics(t *testing.T) {
	t.Helper()
	log, err := logger.NewLogger("dev")
	if err != nil {
		t.Fatal(err)
	}
	_ = gincommon.ObservabilityMiddlewares(gincommon.Config{
		Logger: log, ServiceName: "workflow-definition-service", BuildVersion: "test", Domain: observability.Domain, Environment: "test",
	})
	observability.Register()
}

func oversizedJSON(field string) string {
	return `{"` + field + `":true,"pad":"` + strings.Repeat("x", 2<<20) + `"}`
}

func TestClients_RefuseAnOversizedResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(oversizedJSON("has_active")))
	}))
	defer srv.Close()

	if _, _, err := NewExecutionClient(srv.URL, "t", time.Second).CheckActiveInstances(context.Background(), uuid.New(), uuid.New()); err == nil {
		t.Error("execution client decoded a 2 MiB body")
	}

	eligible := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(oversizedJSON("eligible")))
	}))
	defer eligible.Close()
	if ok, err := NewMembershipClient(eligible.URL, time.Second).CheckEligibility(context.Background(), uuid.New(), uuid.New(), "d", "r"); err == nil || ok {
		t.Errorf("membership client decoded a 2 MiB body: eligible=%v err=%v", ok, err)
	}
}

func TestClients_StartAClientSpanOfTheirOwn(t *testing.T) {
	var traceparent string
	srv := captureTraceparent(&traceparent)
	defer srv.Close()
	ctx := tracedContext(t)
	caller := trace.SpanFromContext(ctx).SpanContext()

	for name, call := range map[string]func(){
		"execution": func() {
			_, _ = NewExecutionClient(srv.URL, "t", time.Second).try(ctx, http.MethodGet, srv.URL+"/x", nil, http.StatusOK, nil)
		},
		"membership": func() {
			_, _, _ = NewMembershipClient(srv.URL, time.Second).tryCheckEligibility(ctx, srv.URL, uuid.New())
		},
		"secrets": func() {
			_ = NewSecretsClient(srv.URL, "t", "secret", time.Second).Delete(ctx, "some/path")
		},
	} {
		traceparent = ""
		call()
		parts := strings.Split(traceparent, "-")
		if len(parts) != 4 {
			t.Errorf("%s: traceparent = %q", name, traceparent)
			continue
		}
		if parts[1] != caller.TraceID().String() {
			t.Errorf("%s: the call left the caller's trace", name)
		}
		if parts[2] == caller.SpanID().String() {
			t.Errorf("%s: no client span was started, the caller's own span id went out", name)
		}
	}
}

func TestClients_RecordTheDependencyHistogram(t *testing.T) {
	registerMetrics(t)
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "active-instances"):
			_, _ = w.Write([]byte(`{"has_active":false,"count":0}`))
		case strings.Contains(r.URL.Path, "eligibility"):
			_, _ = w.Write([]byte(`{"eligible":true}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer ok.Close()
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()
	ctx := context.Background()

	before := map[string]uint64{}
	keys := [][3]string{
		{"execution", "check_active_instances", "success"},
		{"execution", "pause_user_tasks", "error"},
		{"membership", "check_eligibility", "success"},
		{"openbao", "write_secret", "success"},
		{"openbao", "delete_secret", "error"},
	}
	for _, k := range keys {
		before[k[0]+k[1]+k[2]] = dependencySeries(t, k[0], k[1], k[2])
	}

	_, _, _ = NewExecutionClient(ok.URL, "t", time.Second).CheckActiveInstances(ctx, uuid.New(), uuid.New())
	_ = NewExecutionClient(failing.URL, "t", time.Second).PauseUserTasks(ctx, uuid.New(), uuid.New())
	_, _ = NewMembershipClient(ok.URL, time.Second).CheckEligibility(ctx, uuid.New(), uuid.New(), "d", "r")
	_ = NewSecretsClient(ok.URL, "t", "secret", time.Second).Write(ctx, "p", map[string]string{"k": "v"})
	_ = NewSecretsClient(failing.URL, "t", "secret", time.Second).Delete(ctx, "p")

	for _, k := range keys {
		if got := dependencySeries(t, k[0], k[1], k[2]); got != before[k[0]+k[1]+k[2]]+1 {
			t.Errorf("dependency=%s operation=%s outcome=%s: %d samples, want one more than %d", k[0], k[1], k[2], got, before[k[0]+k[1]+k[2]])
		}
	}
}
