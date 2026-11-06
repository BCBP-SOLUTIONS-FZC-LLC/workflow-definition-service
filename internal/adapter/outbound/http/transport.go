package http

import (
	"io"
	"net/http"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

const (
	clientTraceScope = "workflow-definition-service/http-client"
	maxResponseBody  = 1 << 20
	maxErrorSnippet  = 1 << 10
)

func newClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: traceTransport{base: http.DefaultTransport, tracer: gincommon.NewSpanTracer(clientTraceScope)}}
}

type traceTransport struct {
	base   http.RoundTripper
	tracer *gincommon.SpanTracer
}

func (t traceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, end := t.tracer.StartSpan(req.Context(), "HTTP "+req.Method)
	defer end()
	out := req.Clone(ctx)
	gincommon.InjectTraceContext(ctx, out.Header)
	resp, err := t.base.RoundTrip(out)
	if err != nil {
		gincommon.RecordSpanError(ctx, err, "round trip failed")
	}
	return resp, err //nolint:wrapcheck // http.Client reads Timeout() off the RoundTrip error itself
}

func limitedBody(body io.Reader) io.Reader {
	return io.LimitReader(body, maxResponseBody)
}

func errorSnippet(body io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(body, maxErrorSnippet))
	return string(b)
}

func observed(dependency, operation string, call func() error) error {
	start := time.Now()
	err := call()
	observability.ObserveDependency(dependency, operation, err, time.Since(start))
	return err
}
