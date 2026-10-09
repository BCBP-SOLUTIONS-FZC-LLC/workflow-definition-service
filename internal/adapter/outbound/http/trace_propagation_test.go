package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
)

func tracedContext(t *testing.T) context.Context {
	t.Helper()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "none")
	shutdown, err := gincommon.InitTracingWithConfig(gincommon.TracingConfig{
		ServiceName: "workflow-definition-service", BuildVersion: "test", Domain: "workflow", Environment: "test",
	})
	if err != nil {
		t.Fatalf("init tracing: %v", err)
	}
	t.Cleanup(shutdown)
	ctx, span := gincommon.NewTracer("test").Start(context.Background(), "caller")
	t.Cleanup(func() { span.End() })
	return ctx
}

func captureTraceparent(got *string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = r.Header.Get("Traceparent")
		w.WriteHeader(http.StatusOK)
	}))
}

func TestExecutionClient_PropagatesTraceContext(t *testing.T) {
	var traceparent string
	srv := captureTraceparent(&traceparent)
	defer srv.Close()

	c := NewExecutionClient(srv.URL, "token", time.Second)
	_, _ = c.try(tracedContext(t), http.MethodGet, srv.URL+"/x", nil, http.StatusOK, nil)

	if traceparent == "" {
		t.Error("traceparent header missing on the Execution Service call")
	}
}

func TestMembershipClient_PropagatesTraceContext(t *testing.T) {
	var traceparent string
	srv := captureTraceparent(&traceparent)
	defer srv.Close()

	c := &MembershipClient{baseURL: srv.URL, client: srv.Client()}
	_, _, _ = c.tryCheckEligibility(tracedContext(t), srv.URL)

	if traceparent == "" {
		t.Error("traceparent header missing on the membership call")
	}
}
