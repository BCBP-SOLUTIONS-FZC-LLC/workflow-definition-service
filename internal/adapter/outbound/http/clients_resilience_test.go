package http

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type resilienceRoundTripFunc func(*http.Request) (*http.Response, error)

func (f resilienceRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const secretsResilienceToken = "s3cr3t-token-value"

func resilienceBlockingServer(t *testing.T, calls *atomic.Int32, onRequest func()) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if onRequest != nil {
			onRequest()
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	return srv
}

func TestExecutionClient_CallerCancelsWhileRequestInFlight_DoesNotRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	srv := resilienceBlockingServer(t, &calls, cancel)

	_, _, err := NewExecutionClient(srv.URL, "tok", 30*time.Second).
		CheckActiveInstances(ctx, uuid.New(), uuid.New())

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(1), calls.Load(), "a cancelled caller must not trigger another attempt")
}

func TestExecutionClient_CallerCancelsDuringBackoff_StopsBeforeNextAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	var roundTrips atomic.Int32
	client := NewExecutionClient(srv.URL, "tok", 30*time.Second)
	base := client.client.Transport
	client.client.Transport = resilienceRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		roundTrips.Add(1)
		resp, err := base.RoundTrip(r)
		cancel()
		return resp, err
	})

	err := client.PauseUserTasks(ctx, uuid.New(), uuid.New())

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(1), roundTrips.Load(), "no attempt may start after the caller cancelled during the wait")
}

func TestExecutionClient_ClientTimeoutWithLiveContext_RetriesThenFails(t *testing.T) {
	var serverCalls, attempts atomic.Int32
	srv := resilienceBlockingServer(t, &serverCalls, nil)
	client := NewExecutionClient(srv.URL, "tok", 40*time.Millisecond)
	base := client.client.Transport
	client.client.Transport = resilienceRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts.Add(1)
		return base.RoundTrip(r)
	})

	_, _, err := client.CheckActiveInstances(context.Background(), uuid.New(), uuid.New())

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	var netErr net.Error
	require.True(t, errors.As(err, &netErr), "the timeout must stay visible in the error chain")
	assert.True(t, netErr.Timeout())
	assert.Equal(t, int32(maxExecutionAttempts), attempts.Load())
}

func TestSecretsClient_TransportFailure_ReturnsUpstreamUnavailableWithoutRetry(t *testing.T) {
	const refusedURL = "http://127.0.0.1:1"

	var serverCalls atomic.Int32
	slow := resilienceBlockingServer(t, &serverCalls, nil)

	cases := []struct {
		name    string
		addr    string
		timeout time.Duration
		slow    bool
	}{
		{name: "connection refused", addr: refusedURL, timeout: time.Second},
		{name: "client timeout", addr: slow.URL, timeout: 40 * time.Millisecond, slow: true},
	}
	operations := []struct {
		name string
		run  func(c *SecretsClient) error
	}{
		{"write", func(c *SecretsClient) error {
			return c.Write(context.Background(), "connectors/tenant-a/mail", map[string]string{"k": "v"})
		}},
		{"delete", func(c *SecretsClient) error {
			return c.Delete(context.Background(), "connectors/tenant-a/mail")
		}},
	}
	for _, tc := range cases {
		for _, op := range operations {
			t.Run(tc.name+"/"+op.name, func(t *testing.T) {
				var attempts atomic.Int32
				client := NewSecretsClient(tc.addr, secretsResilienceToken, "secret", tc.timeout)
				base := client.client.Transport
				client.client.Transport = resilienceRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					attempts.Add(1)
					return base.RoundTrip(r)
				})
				err := op.run(client)

				require.Error(t, err)
				assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
				assert.Contains(t, err.Error(), "connectors/tenant-a/mail")
				assert.NotContains(t, err.Error(), secretsResilienceToken)
				if tc.slow {
					var netErr net.Error
					require.True(t, errors.As(err, &netErr))
					assert.True(t, netErr.Timeout())
				}
				assert.Equal(t, int32(1), attempts.Load(), "a failed secrets call is not retried")
			})
		}
	}
}
