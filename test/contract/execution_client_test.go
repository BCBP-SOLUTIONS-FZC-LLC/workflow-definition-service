package contract_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	outboundhttp "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/contract"
)

const executionSpec = "../../api/contracts/execution-internal-openapi.yaml"

// executionServer answers each call with the next of replies, after holding
// the request to the copy of the Execution Service's spec.
func executionServer(t *testing.T, replies ...func(w http.ResponseWriter)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	return executionServerFor(t, uuid.Nil, replies...)
}

// executionServerFor is executionServer that also requires the x-tenant-id
// header to equal wantTenant; uuid.Nil skips that check.
func executionServerFor(t *testing.T, wantTenant uuid.UUID, replies ...func(w http.ResponseWriter)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	spec := contract.Load(t, executionSpec)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1))
		spec.Request(t, r)
		if r.Header.Get("x-user-id") != "iam-system" || r.Header.Get("x-tenant-roles") != "iam-system" || r.Header.Get("x-tenant-id") == "" {
			t.Errorf("caller identity headers = %q, %q, %q", r.Header.Get("x-user-id"), r.Header.Get("x-tenant-roles"), r.Header.Get("x-tenant-id"))
		}
		if wantTenant != uuid.Nil && r.Header.Get("x-tenant-id") != wantTenant.String() {
			t.Errorf("x-tenant-id = %q, want the request's tenant %q", r.Header.Get("x-tenant-id"), wantTenant)
		}
		reply := replies[len(replies)-1]
		if n <= len(replies) {
			reply = replies[n-1]
		}
		reply(w)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func reply(status int, body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		if body != "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func executionClient(url string) *outboundhttp.ExecutionClient {
	return outboundhttp.NewExecutionClient(url, time.Second)
}

func TestExecutionClient_ActiveInstancesReadsTheSpecExample(t *testing.T) {
	example := contract.Load(t, executionSpec).Example(t, http.MethodGet, "/api/v1/internal/workflows/{workflow_id}/active-instances", http.StatusOK)
	tenant := uuid.New()
	srv, _ := executionServerFor(t, tenant, reply(http.StatusOK, string(example)))

	hasActive, count, err := executionClient(srv.URL).CheckActiveInstances(context.Background(), tenant, uuid.New())
	if err != nil || !hasActive || count != 2 {
		t.Errorf("CheckActiveInstances = %v, %d, %v; want the example's true, 2", hasActive, count, err)
	}
}

func TestExecutionClient_PauseUserTasks(t *testing.T) {
	tenant := uuid.New()
	srv, calls := executionServerFor(t, tenant, reply(http.StatusNoContent, ""))
	if err := executionClient(srv.URL).PauseUserTasks(context.Background(), tenant, uuid.New(), nil); err != nil {
		t.Errorf("PauseUserTasks: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("%d calls, want 1", calls.Load())
	}
}

func TestExecutionClient_Errors(t *testing.T) {
	problem := `{"type":"https://errors.bcbp.io/execution/x","title":"x","status":0,"detail":"x","instance":"/x","code":"X"}`
	for _, tc := range []struct {
		name      string
		replies   []func(http.ResponseWriter)
		wantErr   bool
		wantCalls int32
	}{
		{"a 4xx is not retried", []func(http.ResponseWriter){reply(http.StatusBadRequest, problem)}, true, 1},
		{"a 5xx is retried three times", []func(http.ResponseWriter){reply(http.StatusInternalServerError, problem)}, true, 3},
		{"a 5xx then success recovers", []func(http.ResponseWriter){reply(http.StatusServiceUnavailable, problem), reply(http.StatusNoContent, "")}, false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := executionServer(t, tc.replies...)
			err := executionClient(srv.URL).PauseUserTasks(context.Background(), uuid.New(), uuid.New(), nil)
			if tc.wantErr != (err != nil) || (err != nil && !errors.Is(err, domain.ErrUpstreamUnavailable)) {
				t.Errorf("err = %v, want an ErrUpstreamUnavailable: %v", err, tc.wantErr)
			}
			if calls.Load() != tc.wantCalls {
				t.Errorf("%d calls, want %d", calls.Load(), tc.wantCalls)
			}
		})
	}
}

func TestExecutionClient_ActiveInstancesRefusesAMalformedBody(t *testing.T) {
	srv, _ := executionServer(t, reply(http.StatusOK, `{"count":1}`))
	if _, _, err := executionClient(srv.URL).CheckActiveInstances(context.Background(), uuid.New(), uuid.New()); !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Errorf("err = %v, want ErrUpstreamUnavailable for a body without has_active", err)
	}
}

func TestExecutionClient_UnreachableIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	if _, _, err := executionClient(srv.URL).CheckActiveInstances(context.Background(), uuid.New(), uuid.New()); !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Errorf("err = %v, want ErrUpstreamUnavailable", err)
	}
}
