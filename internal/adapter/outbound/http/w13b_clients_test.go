package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const w13bExecutionToken = "w13b-execution-token"

type w13bSeen struct {
	method, path, query string
	header              http.Header
	at                  time.Time
}

type w13bServer struct {
	*httptest.Server
	mu   sync.Mutex
	seen []w13bSeen
}

func w13bNewServer(t *testing.T, answer func(n int, w http.ResponseWriter)) *w13bServer {
	t.Helper()
	s := &w13bServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, w13bSeen{
			method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, header: r.Header.Clone(), at: time.Now(),
		})
		n := len(s.seen)
		s.mu.Unlock()
		answer(n, w)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *w13bServer) requests() []w13bSeen {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]w13bSeen(nil), s.seen...)
}

func w13bStatus(status int, body string) func(int, http.ResponseWriter) {
	return func(_ int, w http.ResponseWriter) {
		if body != "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestMembershipClient_CheckEligibility_AnAnswerThatIsNoVerdictIsOneCallAndUnavailable(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		wantInErr string
	}{
		{name: "400 bad request", status: http.StatusBadRequest, wantInErr: "membership returned 400"},
		{name: "401 unauthorized", status: http.StatusUnauthorized, wantInErr: "membership returned 401"},
		{name: "403 forbidden", status: http.StatusForbidden, wantInErr: "membership returned 403"},
		{name: "404 not found", status: http.StatusNotFound, wantInErr: "membership returned 404"},
		{name: "409 conflict", status: http.StatusConflict, wantInErr: "membership returned 409"},
		{name: "204 without a body", status: http.StatusNoContent, wantInErr: "membership returned 204"},
		{name: "200 with only a reason", status: http.StatusOK, body: `{"reason":"zzq_reason"}`, wantInErr: "no eligible flag"},
		{name: "200 with a null flag", status: http.StatusOK, body: `{"eligible":null,"reason":"zzq_reason"}`, wantInErr: "no eligible flag"},
		{name: "200 with an empty object", status: http.StatusOK, body: `{}`, wantInErr: "no eligible flag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := w13bNewServer(t, w13bStatus(tc.status, tc.body))
			tenant, user := uuid.New(), uuid.New()

			got, err := NewMembershipClient(srv.URL, 5*time.Second).CheckEligibility(context.Background(), tenant, user, "zzq-dept", "approver")

			require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.Contains(t, err.Error(), tc.wantInErr)
			assert.Equal(t, domain.Eligibility{}, got)
			reqs := srv.requests()
			require.Len(t, reqs, 1, "an answer that is no verdict is not retried")
			assert.Equal(t, http.MethodGet, reqs[0].method)
			assert.Equal(t, "/api/v1/internal/tenants/"+tenant.String()+"/members/"+user.String()+"/eligibility", reqs[0].path)
			assert.Equal(t, "department_id=zzq-dept&required_level=approver", reqs[0].query)
			assert.Equal(t, tenant.String(), reqs[0].header.Get("x-tenant-id"))
			assert.Equal(t, systemPrincipal, reqs[0].header.Get("x-user-id"))
			assert.Equal(t, systemPrincipal, reqs[0].header.Get("x-tenant-roles"))
		})
	}
}

func TestMembershipClient_CheckEligibility_AFalseVerdictIsAnAnswerAndNotAnError(t *testing.T) {
	srv := w13bNewServer(t, w13bStatus(http.StatusOK, `{"eligible":false,"reason":"zzq_reason"}`))

	got, err := NewMembershipClient(srv.URL, 5*time.Second).CheckEligibility(context.Background(), uuid.New(), uuid.New(), "zzq-dept", "approver")

	require.NoError(t, err)
	assert.Equal(t, domain.Eligibility{Eligible: false, Reason: "zzq_reason"}, got)
	assert.Len(t, srv.requests(), 1)
}

func TestExecutionClient_CheckActiveInstances_5xxOnEveryAttemptIsThreeIdenticalCallsThenUnavailable(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			srv := w13bNewServer(t, w13bStatus(status, `{"code":"ZZQ"}`))
			tenant, workflow := uuid.New(), uuid.New()

			hasActive, count, err := NewExecutionClient(srv.URL, w13bExecutionToken, 5*time.Second).
				CheckActiveInstances(context.Background(), tenant, workflow)

			require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.Contains(t, err.Error(), "check active instances")
			assert.Contains(t, err.Error(), "status "+strconv.Itoa(status))
			assert.NotContains(t, err.Error(), w13bExecutionToken)
			assert.False(t, hasActive)
			assert.Zero(t, count)
			reqs := srv.requests()
			require.Len(t, reqs, maxExecutionAttempts)
			for _, r := range reqs {
				assert.Equal(t, http.MethodGet, r.method)
				assert.Equal(t, "/api/v1/internal/workflows/"+workflow.String()+"/active-instances", r.path)
				assert.Equal(t, "tenant_id="+tenant.String(), r.query)
				assert.Equal(t, w13bExecutionToken, r.header.Get(internalTokenHeader))
			}
		})
	}
}

func TestExecutionClient_CheckActiveInstances_AnAnswerOtherThan200IsOneCallAndUnavailable(t *testing.T) {
	for _, status := range []int{
		http.StatusNoContent, http.StatusBadRequest, http.StatusUnauthorized,
		http.StatusForbidden, http.StatusNotFound, http.StatusConflict,
	} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			srv := w13bNewServer(t, w13bStatus(status, `{"has_active":true,"count":4}`))

			hasActive, count, err := NewExecutionClient(srv.URL, w13bExecutionToken, 5*time.Second).
				CheckActiveInstances(context.Background(), uuid.New(), uuid.New())

			require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.Contains(t, err.Error(), "status "+strconv.Itoa(status))
			assert.False(t, hasActive, "the body of a refused answer is not read")
			assert.Zero(t, count)
			assert.Len(t, srv.requests(), 1, "a status other than 200 or 5xx is not retried")
		})
	}
}

func TestExecutionClient_InternalTokenHeaderIsSentOnlyWhenATokenIsConfigured(t *testing.T) {
	calls := []struct {
		name string
		run  func(c *ExecutionClient) error
		ok   func(w http.ResponseWriter)
	}{
		{
			name: "check active instances",
			run: func(c *ExecutionClient) error {
				_, _, err := c.CheckActiveInstances(context.Background(), uuid.New(), uuid.New())
				return err
			},
			ok: func(w http.ResponseWriter) { _, _ = w.Write([]byte(`{"has_active":false,"count":0}`)) },
		},
		{
			name: "pause user tasks",
			run: func(c *ExecutionClient) error {
				return c.PauseUserTasks(context.Background(), uuid.New(), uuid.New(), nil)
			},
			ok: func(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) },
		},
	}
	tokens := []struct {
		name, token string
		wantSent    bool
	}{
		{name: "configured token", token: w13bExecutionToken, wantSent: true},
		{name: "empty token", token: "", wantSent: false},
	}
	for _, call := range calls {
		for _, tk := range tokens {
			t.Run(call.name+"/"+tk.name, func(t *testing.T) {
				t.Parallel()
				srv := w13bNewServer(t, func(_ int, w http.ResponseWriter) { call.ok(w) })

				err := call.run(NewExecutionClient(srv.URL, tk.token, 5*time.Second))

				require.NoError(t, err)
				reqs := srv.requests()
				require.Len(t, reqs, 1)
				values, present := reqs[0].header[http.CanonicalHeaderKey(internalTokenHeader)]
				if tk.wantSent {
					assert.Equal(t, []string{tk.token}, values)
					return
				}
				assert.False(t, present, "no token is configured, so the header is not sent: %v", values)
			})
		}
	}
}

func TestExecutionClient_CheckActiveInstances_WaitsAtLeastFiftyThenOneHundredMillisecondsBetweenAttempts(t *testing.T) {
	srv := w13bNewServer(t, w13bStatus(http.StatusServiceUnavailable, ""))

	_, _, err := NewExecutionClient(srv.URL, w13bExecutionToken, 5*time.Second).
		CheckActiveInstances(context.Background(), uuid.New(), uuid.New())

	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	reqs := srv.requests()
	require.Len(t, reqs, maxExecutionAttempts)
	assert.GreaterOrEqual(t, reqs[1].at.Sub(reqs[0].at), retryBackoff(1), "the first wait is the first backoff step")
	assert.GreaterOrEqual(t, reqs[2].at.Sub(reqs[1].at), retryBackoff(2), "the second wait is the second backoff step")
	assert.Equal(t, 50*time.Millisecond, retryBackoff(1))
	assert.Equal(t, 100*time.Millisecond, retryBackoff(2))
}

func TestExecutionClient_CheckActiveInstances_RecordsOneObservationPerCallLabelledByOutcome(t *testing.T) {
	registerMetrics(t)
	cases := []struct {
		name        string
		answer      func(int, http.ResponseWriter)
		wantAttempt int
		wantOK      uint64
		wantErr     uint64
	}{
		{name: "an answer", answer: w13bStatus(http.StatusOK, `{"has_active":false,"count":0}`), wantAttempt: 1, wantOK: 1},
		{name: "a 5xx on all three attempts", answer: w13bStatus(http.StatusInternalServerError, ""), wantAttempt: 3, wantErr: 1},
		{name: "a 5xx then an answer", answer: func(n int, w http.ResponseWriter) {
			if n == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(`{"has_active":true,"count":1}`))
		}, wantAttempt: 2, wantOK: 1},
		{name: "a 404", answer: w13bStatus(http.StatusNotFound, ""), wantAttempt: 1, wantErr: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := w13bNewServer(t, tc.answer)
			okBefore := dependencySeries(t, "execution", "check_active_instances", "success")
			errBefore := dependencySeries(t, "execution", "check_active_instances", "error")

			_, _, _ = NewExecutionClient(srv.URL, w13bExecutionToken, 5*time.Second).
				CheckActiveInstances(context.Background(), uuid.New(), uuid.New())

			assert.Len(t, srv.requests(), tc.wantAttempt)
			assert.Equal(t, okBefore+tc.wantOK, dependencySeries(t, "execution", "check_active_instances", "success"))
			assert.Equal(t, errBefore+tc.wantErr, dependencySeries(t, "execution", "check_active_instances", "error"))
		})
	}
}
