package contract_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestExecutionClient_ActiveInstancesRefusesAnUndecodableBodyWithoutRetrying(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "plain text", body: "service is warming up"},
		{name: "truncated json", body: `{"has_active":tr`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, calls := executionServer(t, reply(http.StatusOK, tc.body))

			_, _, err := executionClient(srv.URL).CheckActiveInstances(context.Background(), uuid.New(), uuid.New())

			if !errors.Is(err, domain.ErrUpstreamUnavailable) {
				t.Errorf("err = %v, want ErrUpstreamUnavailable", err)
			}
			if err != nil && !strings.Contains(err.Error(), "decode response") {
				t.Errorf("err = %v, want it to name the decode failure", err)
			}
			if calls.Load() != 1 {
				t.Errorf("%d calls, want 1: a body that cannot be decoded is not retried", calls.Load())
			}
		})
	}
}

func TestExecutionClient_ActiveInstancesWithAnUnusableBaseURLIsUnavailableWithoutRetrying(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		baseURL string
	}{
		{name: "control character in the host", baseURL: "http://exec\x7f.internal"},
		{name: "space in the host", baseURL: "http://exec utor.internal"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			start := time.Now()
			_, _, err := executionClient(tc.baseURL).CheckActiveInstances(context.Background(), uuid.New(), uuid.New())
			elapsed := time.Since(start)

			if !errors.Is(err, domain.ErrUpstreamUnavailable) {
				t.Errorf("err = %v, want ErrUpstreamUnavailable", err)
			}
			if err != nil && !strings.Contains(err.Error(), "build request") {
				t.Errorf("err = %v, want it to name the request that could not be built", err)
			}
			if elapsed >= 100*time.Millisecond {
				t.Errorf("took %v: a request that cannot be built must not wait out the retry backoff", elapsed)
			}
		})
	}
}
