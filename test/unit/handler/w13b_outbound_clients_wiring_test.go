package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	outboundhttp "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type w13bExecutionCall struct {
	method, path, query string
	header              http.Header
}

type w13bExecutionServer struct {
	*httptest.Server
	mu    sync.Mutex
	calls []w13bExecutionCall
}

func w13bNewExecutionServer(t *testing.T, status int, body string) *w13bExecutionServer {
	t.Helper()
	s := &w13bExecutionServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls = append(s.calls, w13bExecutionCall{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, header: r.Header.Clone()})
		s.mu.Unlock()
		if body != "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *w13bExecutionServer) seen() []w13bExecutionCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]w13bExecutionCall(nil), s.calls...)
}

func w13bArchivePath() string { return "/api/v1/workflows/" + testWFID.String() + "/archive" }

func w13bArchive(t *testing.T, executionURL string, wantArchived bool) *httptest.ResponseRecorder {
	t.Helper()
	ctrl := gomock.NewController(t)
	workflows := mocks.NewMockWorkflowRepository(ctrl)
	versions := mocks.NewMockWorkflowVersionRepository(ctrl)
	tx := mocks.NewMockTransactor(ctrl)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).AnyTimes().
		DoAndReturn(func(ctx context.Context, f func(context.Context) error) error { return f(ctx) })
	active := testVerID
	workflows.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).AnyTimes().
		Return(&domain.Workflow{ID: testWFID, TenantID: testTenantID, ActiveVersionID: &active}, nil)
	if wantArchived {
		versions.EXPECT().Archive(gomock.Any(), testTenantID, testVerID).Return(nil)
		workflows.EXPECT().UpdateActiveVersion(gomock.Any(), testTenantID, testWFID, (*uuid.UUID)(nil)).Return(nil)
	}
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor: tx, Workflows: workflows, Versions: versions,
		Execution: outboundhttp.NewExecutionClient(executionURL, 5*time.Second),
	})
	router := newRouter(handler.New(handler.Services{Workflows: svc}))
	return do(router, adminReq(http.MethodPost, w13bArchivePath(), nil))
}

func w13bAssert503(t *testing.T, w *httptest.ResponseRecorder, path string, hidden ...string) {
	t.Helper()
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, w10dProblemBase+"service-unavailable", body["type"])
	assert.Equal(t, "UPSTREAM_UNAVAILABLE", body["code"])
	assert.Equal(t, "an upstream dependency is temporarily unavailable", body["detail"])
	assert.EqualValues(t, http.StatusServiceUnavailable, body["status"])
	assert.Equal(t, path, body["instance"])
	for _, h := range hidden {
		assert.NotContains(t, w.Body.String(), h)
	}
}

func TestArchiveWorkflow_ExecutionServiceAnswering5xxOnEveryAttempt_Is503AfterThreeCalls(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			srv := w13bNewExecutionServer(t, status, `{"code":"ZZQ"}`)

			w := w13bArchive(t, srv.URL, false)

			w13bAssert503(t, w, w13bArchivePath(), srv.URL)
			calls := srv.seen()
			require.Len(t, calls, 3)
			for _, c := range calls {
				assert.Equal(t, http.MethodGet, c.method)
				assert.Equal(t, "/api/v1/internal/workflows/"+testWFID.String()+"/active-instances", c.path)
				assert.Equal(t, "tenant_id="+testTenantID.String(), c.query)
				assert.Equal(t, testTenantID.String(), c.header.Get("x-tenant-id"))
			}
		})
	}
}

func TestArchiveWorkflow_ExecutionServiceAnsweringOtherThan200_Is503AfterOneCall(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusNoContent} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			srv := w13bNewExecutionServer(t, status, `{"has_active":false,"count":0}`)

			w := w13bArchive(t, srv.URL, false)

			w13bAssert503(t, w, w13bArchivePath(), srv.URL)
			assert.Len(t, srv.seen(), 1, "a status other than 200 or 5xx is not retried")
		})
	}
}

func TestArchiveWorkflow_ExecutionCallCarriesTheSystemCallerIdentity_Returns200(t *testing.T) {
	srv := w13bNewExecutionServer(t, http.StatusOK, `{"has_active":false,"count":0}`)

	w := w13bArchive(t, srv.URL, true)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "ARCHIVED", body["status"])
	calls := srv.seen()
	require.Len(t, calls, 1)
	assert.Equal(t, "iam-system", calls[0].header.Get("x-user-id"))
	assert.Equal(t, "iam-system", calls[0].header.Get("x-tenant-roles"))
	assert.Equal(t, testTenantID.String(), calls[0].header.Get("x-tenant-id"))
	_, token := calls[0].header["X-Internal-Token"]
	assert.False(t, token, "the shared internal token is gone")
}

func TestPublishVersion_MembershipAnswerThatIsNoVerdict_Is503AfterOneCall(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "400", status: http.StatusBadRequest},
		{name: "403", status: http.StatusForbidden},
		{name: "404", status: http.StatusNotFound},
		{name: "200 with only a reason", status: http.StatusOK, body: `{"reason":"zzq_reason"}`},
		{name: "200 with a null flag", status: http.StatusOK, body: `{"eligible":null}`},
		{name: "200 with an empty object", status: http.StatusOK, body: `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := w10dNewMembershipServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			rig := w10dNewPublishRig(t, srv.URL, 5*time.Second)

			w := rig.publish(context.Background())

			w10dAssertPublish503(t, w, srv.URL)
			calls := srv.seen()
			require.Len(t, calls, 1, "an answer that is no verdict is not retried")
			rig.wantEligibilityRequest(t, calls[0])
		})
	}
}

func TestWriteConnectorCredential_OpenBaoAnswering200Or204_Returns201AndSendsTheDocumentedWrite(t *testing.T) {
	const token = "w13b-vault-token"
	const secretValue = "w13b-secret-value"
	for _, status := range []int{http.StatusOK, http.StatusNoContent} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var method, path, gotToken, body string
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				mu.Lock()
				calls++
				method, path, gotToken, body = r.Method, r.URL.Path, r.Header.Get("X-Vault-Token"), string(b)
				mu.Unlock()
				w.WriteHeader(status)
			}))
			defer srv.Close()
			svc := service.NewConnectorService(service.ConnectorDeps{
				Secrets: outboundhttp.NewSecretsClient(srv.URL, token, "secret", 5*time.Second),
			})
			router := newRouter(handler.New(handler.Services{Connectors: svc}))
			reqBody := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": secretValue}

			w := do(router, adminReq(http.MethodPost, credentialWritePath, reqBody))

			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			wantPath := "connectors/" + testTenantID.String() + "/send-email/apiKey"
			assert.Equal(t, map[string]any{"secret_path": wantPath}, decodeProblem(t, w))
			assert.NotContains(t, w.Body.String(), secretValue)
			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, 1, calls)
			assert.Equal(t, http.MethodPost, method)
			assert.Equal(t, "/v1/secret/data/"+wantPath, path)
			assert.Equal(t, token, gotToken)
			assert.Equal(t, `{"data":{"apiKey":"`+secretValue+`"}}`, body)
		})
	}
}
