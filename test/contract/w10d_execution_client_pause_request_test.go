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
	tokens      []string
	hasToken    bool
	body        string
}

func TestExecutionClient_PauseUserTasks_SendsOneExactRequestAndOmitsAnEmptyToken(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	userID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	cases := []struct {
		name       string
		token      string
		wantTokens []string
		holdToSpec bool
	}{
		{name: "a configured token goes out once under x-internal-token", token: "w10d-execution-token", wantTokens: []string{"w10d-execution-token"}, holdToSpec: true},
		{name: "an empty token leaves the header off the request", token: "", wantTokens: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
					contentType: r.Header.Get("Content-Type"), tokens: r.Header.Values("x-internal-token"),
					hasToken: has, body: string(b),
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(srv.Close)

			client := outboundhttp.NewExecutionClient(srv.URL, tc.token, time.Second)
			require.NoError(t, client.PauseUserTasks(context.Background(), tenantID, userID, nil))

			assert.Equal(t, int32(1), calls.Load(), "an answer of 204 ends the call after one request")
			require.NotNil(t, captured)
			if tc.holdToSpec {
				spec.Request(t, captured)
			}
			assert.Equal(t, http.MethodPost, seen.method)
			assert.Equal(t, "/api/v1/internal/users/"+userID.String()+"/pause-tasks", seen.path)
			assert.Empty(t, seen.rawQuery)
			assert.Equal(t, "application/json", seen.contentType)
			assert.Equal(t, `{"tenant_id":"`+tenantID.String()+`"}`, seen.body, "a call without a department carries the tenant id and nothing else")
			assert.Equal(t, tc.wantTokens, seen.tokens)
			assert.Equal(t, tc.token != "", seen.hasToken)
		})
	}
}
