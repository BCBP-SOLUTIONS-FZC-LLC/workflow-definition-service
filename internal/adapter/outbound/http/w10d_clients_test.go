package http

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type w10dTransport func(*http.Request) (*http.Response, error)

func (f w10dTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const (
	w10dRefusedURL = "http://127.0.0.1:1"
	w10dMiB        = 1 << 20
)

func w10dPattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}

func w10dPaddedActiveInstancesJSON(total int) string {
	prefix := `{"has_active":true,"count":7,"pad":"`
	suffix := `"}`
	return prefix + strings.Repeat("x", total-len(prefix)-len(suffix)) + suffix
}

func TestRetryBackoff_DoublesFromFiftyMillisecondsAndStopsAtFiveHundred(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 50 * time.Millisecond},
		{2, 100 * time.Millisecond},
		{3, 200 * time.Millisecond},
		{4, 400 * time.Millisecond},
		{5, 500 * time.Millisecond},
		{6, 500 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run("attempt "+strconv.Itoa(tc.attempt), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, retryBackoff(tc.attempt))
		})
	}
}

func TestCatalogClient_ReadPlanTable_PlanCodeIsTrimmedAndLowerCased(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"plans":[
			{"code":"  StArTeR \t","workflow_template_limit":5},
			{"code":" Pro Plan ","workflow_template_limit":null}
		]}`))
	}))
	defer srv.Close()

	table, err := NewCatalogClient(srv.URL, time.Second).ReadPlanTable(context.Background())

	require.NoError(t, err)
	require.Len(t, table, 2)
	require.Contains(t, table, "starter")
	assert.Equal(t, int64(5), *table["starter"])
	require.Contains(t, table, "pro plan", "interior spaces stay, the ends are trimmed")
	assert.Nil(t, table["pro plan"])
}

func TestCatalogClient_ReadPlanTable_ACodeWithNothingButBlanksRefusesTheWholeTable(t *testing.T) {
	cases := map[string]string{
		"empty":                   `""`,
		"spaces":                  `"   "`,
		"tab and newline":         `"\t\n"`,
		"non-breaking space only": `"  "`,
	}
	for name, code := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"plans":[{"code":"starter","workflow_template_limit":5},{"code":` + code + `,"workflow_template_limit":1}]}`))
			}))
			defer srv.Close()

			table, err := NewCatalogClient(srv.URL, time.Second).ReadPlanTable(context.Background())

			require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.Contains(t, err.Error(), "without a code")
			assert.Nil(t, table, "a half-read table must not be returned")
		})
	}
}

func TestCatalogClient_ReadPlanTable_ANon200StatusIsOneCallAndCarriesACutSnippet(t *testing.T) {
	cases := []struct {
		status int
	}{{http.StatusNotFound}, {http.StatusInternalServerError}, {http.StatusBadGateway}}
	for _, tc := range cases {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(strings.Repeat("#", 1024) + "TAILTAIL"))
			}))
			defer srv.Close()

			table, err := NewCatalogClient(srv.URL, time.Second).ReadPlanTable(context.Background())

			require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.Nil(t, table)
			assert.Equal(t, int32(1), calls.Load(), "a refused read is not retried")
			assert.Contains(t, err.Error(), "catalog returned "+strconv.Itoa(tc.status))
			assert.Equal(t, 1024, strings.Count(err.Error(), "#"), "the body snippet is cut at 1024 bytes")
			assert.NotContains(t, err.Error(), "TAIL")
		})
	}
}

func TestExecutionClient_AnUnusableOKBodyIsOneCallAndAnUnavailableError(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"not json", `{bad`},
		{"valid json cut off by the 1 MiB limit", w10dPaddedActiveInstancesJSON(w10dMiB + 1)},
		{"json that is far over the limit", oversizedJSON("has_active")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			has, count, err := NewExecutionClient(srv.URL, "tok", time.Second).CheckActiveInstances(context.Background(), uuid.New(), uuid.New())

			require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.Contains(t, err.Error(), "decode response")
			assert.False(t, has)
			assert.Zero(t, count)
			assert.Equal(t, int32(1), calls.Load(), "a body that cannot be read is not retried")
		})
	}
}

func TestExecutionClient_ABodyEndingExactlyAtOneMiBIsRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(w10dPaddedActiveInstancesJSON(w10dMiB)))
	}))
	defer srv.Close()

	has, count, err := NewExecutionClient(srv.URL, "tok", time.Second).CheckActiveInstances(context.Background(), uuid.New(), uuid.New())

	require.NoError(t, err)
	assert.True(t, has)
	assert.Equal(t, int32(7), count)
}

func TestLimitedBody_ReadsTheFirstMiBAndNothingMore(t *testing.T) {
	cases := []struct {
		name string
		size int
		want int
	}{
		{"one byte under", w10dMiB - 1, w10dMiB - 1},
		{"exactly the limit", w10dMiB, w10dMiB},
		{"one byte over", w10dMiB + 1, w10dMiB},
		{"twice the limit", 2 * w10dMiB, w10dMiB},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := w10dPattern(tc.size)

			got, err := io.ReadAll(limitedBody(bytes.NewReader(in)))

			require.NoError(t, err)
			assert.Equal(t, in[:tc.want], got)
		})
	}
}

func TestErrorSnippet_KeepsTheFirstKiBOfTheBody(t *testing.T) {
	cases := []struct {
		name string
		size int
		want int
	}{
		{"empty", 0, 0},
		{"short", 10, 10},
		{"one byte under", 1023, 1023},
		{"exactly the limit", 1024, 1024},
		{"one byte over", 1025, 1024},
		{"one MiB", w10dMiB, 1024},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := w10dPattern(tc.size)

			got := errorSnippet(bytes.NewReader(in))

			assert.Equal(t, string(in[:tc.want]), got)
		})
	}
}

func TestExecutionClient_PauseUserTasks_AnUnreachableServiceIsAttemptedThreeTimes(t *testing.T) {
	var attempts atomic.Int32
	client := NewExecutionClient(w10dRefusedURL, "tok", time.Second)
	base := client.client.Transport
	client.client.Transport = w10dTransport(func(r *http.Request) (*http.Response, error) {
		attempts.Add(1)
		return base.RoundTrip(r)
	})

	err := client.PauseUserTasks(context.Background(), uuid.New(), uuid.New(), nil)

	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	assert.Contains(t, err.Error(), "pause user tasks")
	assert.Equal(t, int32(3), attempts.Load())
}

func TestExecutionClient_PauseUserTasks_ACallerCancelledBeforeTheSendIsNotRetried(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var attempts atomic.Int32
	client := NewExecutionClient(w10dRefusedURL, "tok", time.Minute)
	base := client.client.Transport
	client.client.Transport = w10dTransport(func(r *http.Request) (*http.Response, error) {
		attempts.Add(1)
		return base.RoundTrip(r)
	})

	err := client.PauseUserTasks(ctx, uuid.New(), uuid.New(), nil)

	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(1), attempts.Load(), "a cancelled caller does not use the remaining attempts")
}

func TestMembershipClient_CheckEligibility_ACancelledCallerGetsAnUnavailableErrorThatNamesTheCancel(t *testing.T) {
	t.Run("cancelled before the request is sent", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			_, _ = w.Write([]byte(`{"eligible":true}`))
		}))
		defer srv.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		got, err := NewMembershipClient(srv.URL, time.Minute).CheckEligibility(ctx, uuid.New(), uuid.New(), "dept", "reviewer")

		require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
		require.ErrorIs(t, err, context.Canceled)
		assert.False(t, got.Eligible)
		assert.Zero(t, calls.Load(), "nothing reaches the service for a caller that is already gone")
	})

	t.Run("cancelled while the first attempt is answered with a 503", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			cancel()
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		got, err := NewMembershipClient(srv.URL, time.Minute).CheckEligibility(ctx, uuid.New(), uuid.New(), "dept", "reviewer")

		require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
		require.ErrorIs(t, err, context.Canceled)
		assert.False(t, got.Eligible)
		assert.Equal(t, int32(1), calls.Load(), "no second attempt starts once the caller is gone")
	})
}

func TestSecretsClient_Write_SendsTheDocumentedRequestAndClassifiesEachAnswer(t *testing.T) {
	const secretValue = "sg-live-w10d-secret-value"
	const token = "w10d-vault-token"
	cases := []struct {
		status  int
		wantErr bool
	}{
		{http.StatusOK, false},
		{http.StatusNoContent, false},
		{http.StatusForbidden, true},
		{http.StatusNotFound, true},
		{http.StatusInternalServerError, true},
	}
	for _, tc := range cases {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			var method, path, contentType, gotToken, body string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				b, _ := io.ReadAll(r.Body)
				method, path, contentType, gotToken, body = r.Method, r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("X-Vault-Token"), string(b)
				w.WriteHeader(tc.status)
				if tc.status >= 400 {
					_, _ = w.Write([]byte("w10d-vault-says-no"))
				}
			}))
			defer srv.Close()
			c := NewSecretsClient(srv.URL+"/", token, "kv", time.Second)

			err := c.Write(context.Background(), "/connectors/tenant-a/mail", map[string]string{"apiKey": secretValue})

			assert.Equal(t, int32(1), calls.Load(), "a secrets write is not retried")
			assert.Equal(t, http.MethodPost, method)
			assert.Equal(t, "/v1/kv/data/connectors/tenant-a/mail", path)
			assert.Equal(t, "application/json", contentType)
			assert.Equal(t, token, gotToken)
			assert.Equal(t, `{"data":{"apiKey":"`+secretValue+`"}}`, body)
			if !tc.wantErr {
				assert.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.False(t, errors.Is(err, domain.ErrUnauthorized) || errors.Is(err, domain.ErrForbidden))
			assert.Contains(t, err.Error(), "status "+strconv.Itoa(tc.status))
			assert.Contains(t, err.Error(), "w10d-vault-says-no")
			assert.NotContains(t, err.Error(), secretValue, "the secret never reaches the error text")
			assert.NotContains(t, err.Error(), token, "the service token never reaches the error text")
		})
	}
}

func TestSecretsClient_Write_ErrorSnippetIsCutAtOneKiB(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Repeat("#", 1024) + "TAILTAIL"))
	}))
	defer srv.Close()

	err := NewSecretsClient(srv.URL, "t", "secret", time.Second).Write(context.Background(), "p", map[string]string{"k": "v"})

	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	assert.Equal(t, 1024, strings.Count(err.Error(), "#"))
	assert.NotContains(t, err.Error(), "TAIL")
}

func TestTraceTransport_SendsAClonedRequestAndReturnsARoundTripErrorUntouched(t *testing.T) {
	boom := errors.New("w10d round trip refused")
	cases := []struct {
		name    string
		answer  func() (*http.Response, error)
		wantErr bool
	}{
		{"a refused round trip", func() (*http.Response, error) { return nil, boom }, true},
		{"an answered round trip", func() (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tracedContext(t)
			var sent *http.Request
			tr := traceTransport{
				base: w10dTransport(func(r *http.Request) (*http.Response, error) {
					sent = r
					return tc.answer()
				}),
				tracer: gincommon.NewSpanTracer("w10d"),
			}
			orig, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.invalid/x", nil)
			require.NoError(t, err)
			orig.Header.Set("X-Keep", "1")

			resp, err := tr.RoundTrip(orig)
			if resp != nil {
				defer func() { _ = resp.Body.Close() }()
			}

			if tc.wantErr {
				require.Same(t, boom, err, "the error comes back untouched so the client can read its Timeout")
			} else {
				require.NoError(t, err)
				require.NotNil(t, resp)
			}
			require.NotNil(t, sent)
			assert.NotSame(t, orig, sent, "the transport must not send the caller's own request")
			assert.NotEmpty(t, sent.Header.Get("Traceparent"))
			assert.Empty(t, orig.Header.Get("Traceparent"), "the caller's request is left without the injected header")
			assert.Equal(t, "1", sent.Header.Get("X-Keep"))
			assert.Equal(t, []string{"1"}, orig.Header.Values("X-Keep"))
		})
	}
}
