package http

import (
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

const (
	maxResponseBody = 1 << 20
	maxErrorSnippet = 1 << 10
)

func newClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: otelhttp.NewTransport(http.DefaultTransport)}
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
