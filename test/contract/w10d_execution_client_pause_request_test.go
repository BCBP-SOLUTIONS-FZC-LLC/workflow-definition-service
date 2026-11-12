package contract_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	outboundhttp "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/contract"
)

type w10dSeenRequest struct {
	method      string
	path        string
	rawQuery    string
	contentType string
	userIDs     []string
	roles       []string
	tenants     []string
	hasToken    bool
	body        string
}

func TestExecutionClient_PauseUserTasks_SendsOneExactRequestWithTheSystemCallerIdentity(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	userID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	spec := contract.Load(t, executionSpec)
	var calls atomic.Int32
	var seen w10dSeenRequest
	var captured *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		captured = r.Clone(context.Background())
		captured.Body = io.NopCloser(bytes.NewReader(b))
		_, has := r.Header["X-Internal-Token"]
		seen = w10dSeenRequest{
			method: r.Method, path: r.URL.Path, rawQuery: r.URL.RawQuery,
			contentType: r.Header.Get("Content-Type"), hasToken: has, body: string(b),
			userIDs: r.Header.Values("x-user-id"), roles: r.Header.Values("x-tenant-roles"), tenants: r.Header.Values("x-tenant-id"),
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	client := outboundhttp.NewExecutionClient(srv.URL, time.Second)
	require.NoError(t, client.PauseUserTasks(context.Background(), tenantID, userID, nil))

	assert.Equal(t, int32(1), calls.Load(), "an answer of 204 ends the call after one request")
	require.NotNil(t, captured)
	spec.Request(t, captured)
	assert.Equal(t, http.MethodPost, seen.method)
	assert.Equal(t, "/api/v1/internal/users/"+userID.String()+"/pause-tasks", seen.path)
	assert.Empty(t, seen.rawQuery)
	assert.Equal(t, "application/json", seen.contentType)
	assert.Equal(t, `{"tenant_id":"`+tenantID.String()+`"}`, seen.body, "a call without a department carries the tenant id and nothing else")
	assert.Equal(t, []string{"iam-system"}, seen.userIDs)
	assert.Equal(t, []string{"iam-system"}, seen.roles)
	assert.Equal(t, []string{tenantID.String()}, seen.tenants)
	assert.False(t, seen.hasToken, "the shared internal token is gone")
}
