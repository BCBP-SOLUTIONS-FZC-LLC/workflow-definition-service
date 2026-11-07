package http

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestSecretsClient_Delete_RefusedConnectionIsAnOutageAndRecordsAnErrorSample(t *testing.T) {
	registerMetrics(t)
	before := dependencySeries(t, "openbao", "delete_secret", "error")
	successBefore := dependencySeries(t, "openbao", "delete_secret", "success")

	c := NewSecretsClient("http://127.0.0.1:1", "sgD-token", "secret", 2*time.Second)
	err := c.Delete(context.Background(), "connectors/t/send-email/apiKey")

	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Fatalf("a refused connection must map to ErrUpstreamUnavailable, got %v", err)
	}
	if got := dependencySeries(t, "openbao", "delete_secret", "error"); got != before+1 {
		t.Errorf("error samples = %d, want %d", got, before+1)
	}
	if got := dependencySeries(t, "openbao", "delete_secret", "success"); got != successBefore {
		t.Errorf("success samples = %d, want unchanged %d", got, successBefore)
	}
}
