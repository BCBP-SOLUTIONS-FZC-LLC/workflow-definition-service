package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	outboundhttp "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const credentialWritePath = "/api/v1/connectors/credentials"

type credentialWriteStore struct {
	mu     sync.Mutex
	writes []credentialWriteCall
}

type credentialWriteCall struct {
	path string
	data map[string]string
}

func (s *credentialWriteStore) Write(_ context.Context, path string, data map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, credentialWriteCall{path: path, data: data})
	return nil
}

func (s *credentialWriteStore) Delete(context.Context, string) error { return nil }

func (s *credentialWriteStore) calls() []credentialWriteCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]credentialWriteCall(nil), s.writes...)
}

func credentialWriteRealHandler(secrets port.SecretsClient) *gin.Engine {
	svc := service.NewConnectorService(service.ConnectorDeps{Secrets: secrets})
	return newRouter(handler.New(handler.Services{Connectors: svc}))
}

func credentialWriteRawReq(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, credentialWritePath, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", testTenantID.String())
	r.Header.Set("x-user-id", testUserID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	return r
}

func credentialWriteInvalidParams(t *testing.T, prob map[string]any) []map[string]any {
	t.Helper()
	raw, _ := prob["invalid_params"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, p := range raw {
		m, ok := p.(map[string]any)
		require.True(t, ok)
		out = append(out, m)
	}
	return out
}

func TestWriteConnectorCredential_MissingIdentity_Returns500(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
	}{
		{name: "no identity headers or middleware"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			h := newConnectorHandler(&fakeConnectorSvc{
				writeFn: func(context.Context, uuid.UUID, string, string, string) (string, error) {
					called = true
					return "", nil
				},
			})
			body := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "x"}

			w := do(newBareRouter(h), adminReq(http.MethodPost, credentialWritePath, body))

			require.Equal(t, http.StatusInternalServerError, w.Code)
			assert.Equal(t, "INTERNAL_ERROR", decodeProblem(t, w)["code"])
			assert.False(t, called, "no credential may be written without an identity")
		})
	}
}

func TestWriteConnectorCredential_UnreadableBody_Returns400(t *testing.T) {
	t.Parallel()

	valid := `{"connector_type":"send-email","field_name":"apiKey","value":"x"}`
	cases := []struct {
		name string
		body string
	}{
		{name: "truncated json", body: `{"connector_type": `},
		{name: "not json at all", body: `connector_type=send-email`},
		{name: "empty body", body: ``},
		{name: "whitespace only body", body: " \n\t "},
		{name: "second json object after the first", body: valid + `{"a":1}`},
		{name: "garbage after the object", body: valid + ` garbage`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			h := newConnectorHandler(&fakeConnectorSvc{
				writeFn: func(context.Context, uuid.UUID, string, string, string) (string, error) {
					called = true
					return "", nil
				},
			})

			w := do(newRouter(h), credentialWriteRawReq(tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, "request body is not valid JSON", prob["detail"])
			assert.False(t, called, "a body that cannot be read must not reach the service")
		})
	}
}

func TestWriteConnectorCredential_WrongFieldType_Returns400(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		body      string
		wantField string
	}{
		{name: "connector_type is a number", body: `{"connector_type":123,"field_name":"apiKey","value":"x"}`, wantField: "connector_type"},
		{name: "field_name is a boolean", body: `{"connector_type":"send-email","field_name":true,"value":"x"}`, wantField: "field_name"},
		{name: "value is an object", body: `{"connector_type":"send-email","field_name":"apiKey","value":{"a":1}}`, wantField: "value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			h := newConnectorHandler(&fakeConnectorSvc{
				writeFn: func(context.Context, uuid.UUID, string, string, string) (string, error) {
					called = true
					return "", nil
				},
			})

			w := do(newRouter(h), credentialWriteRawReq(tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			params := credentialWriteInvalidParams(t, prob)
			require.Len(t, params, 1)
			assert.Equal(t, tc.wantField, params[0]["name"])
			assert.Equal(t, "has the wrong type", params[0]["reason"])
			assert.False(t, called)
		})
	}
}

func TestWriteConnectorCredential_MissingOrNullField_Returns400(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		body      string
		wantField string
	}{
		{name: "connector_type missing", body: `{"field_name":"apiKey","value":"x"}`, wantField: "connector_type"},
		{name: "connector_type empty", body: `{"connector_type":"","field_name":"apiKey","value":"x"}`, wantField: "connector_type"},
		{name: "connector_type null", body: `{"connector_type":null,"field_name":"apiKey","value":"x"}`, wantField: "connector_type"},
		{name: "field_name null", body: `{"connector_type":"send-email","field_name":null,"value":"x"}`, wantField: "field_name"},
		{name: "value null", body: `{"connector_type":"send-email","field_name":"apiKey","value":null}`, wantField: "value"},
		{name: "value empty", body: `{"connector_type":"send-email","field_name":"apiKey","value":""}`, wantField: "value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			h := newConnectorHandler(&fakeConnectorSvc{
				writeFn: func(context.Context, uuid.UUID, string, string, string) (string, error) {
					called = true
					return "", nil
				},
			})

			w := do(newRouter(h), credentialWriteRawReq(tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			params := credentialWriteInvalidParams(t, prob)
			require.Len(t, params, 1)
			assert.Equal(t, tc.wantField, params[0]["name"])
			assert.Equal(t, "is required", params[0]["reason"])
			assert.False(t, called)
		})
	}
}

func TestWriteConnectorCredential_AllowedFieldNameCharacters_Returns201(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		fieldName string
	}{
		{name: "letters", fieldName: "apiKey"},
		{name: "digits only", fieldName: "12345"},
		{name: "hyphen and underscore", fieldName: "api-key_2"},
		{name: "single hyphen", fieldName: "-"},
		{name: "single underscore", fieldName: "_"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := &credentialWriteStore{}
			body := map[string]any{"connector_type": "send-email", "field_name": tc.fieldName, "value": "secret-1"}

			w := do(credentialWriteRealHandler(store), adminReq(http.MethodPost, credentialWritePath, body))

			require.Equal(t, http.StatusCreated, w.Code)
			wantPath := fmt.Sprintf("connectors/%s/send-email/%s", testTenantID, tc.fieldName)
			assert.Equal(t, wantPath, decodeProblem(t, w)["secret_path"])
			assert.NotContains(t, w.Body.String(), "secret-1")
			calls := store.calls()
			require.Len(t, calls, 1)
			assert.Equal(t, wantPath, calls[0].path)
			assert.Equal(t, map[string]string{tc.fieldName: "secret-1"}, calls[0].data)
		})
	}
}

func TestWriteConnectorCredential_UnknownTypeAndBadFieldName_Returns400NamingConnectorType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
	}{
		{name: "both inputs invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := &credentialWriteStore{}
			body := map[string]any{"connector_type": "no-such-connector", "field_name": "../other-tenant/key", "value": "unwritten-secret"}

			w := do(credentialWriteRealHandler(store), adminReq(http.MethodPost, credentialWritePath, body))

			require.Equal(t, http.StatusBadRequest, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "INVALID_INPUT", prob["code"])
			detail, _ := prob["detail"].(string)
			assert.Contains(t, detail, "connector_type")
			assert.NotContains(t, detail, "field_name")
			assert.Empty(t, store.calls(), "rejected input must never reach the secrets store")
			assert.NotContains(t, w.Body.String(), "unwritten-secret")
		})
	}
}

func TestWriteConnectorCredential_ExistingCredential_Returns201AndOverwrites(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
	}{
		{name: "same type and field written twice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := &credentialWriteStore{}
			router := credentialWriteRealHandler(store)
			first := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "old-secret"}
			second := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "new-secret"}

			w1 := do(router, adminReq(http.MethodPost, credentialWritePath, first))
			w2 := do(router, adminReq(http.MethodPost, credentialWritePath, second))

			require.Equal(t, http.StatusCreated, w1.Code)
			require.Equal(t, http.StatusCreated, w2.Code)
			calls := store.calls()
			require.Len(t, calls, 2)
			assert.Equal(t, calls[0].path, calls[1].path, "the second write targets the same secret")
			assert.Equal(t, map[string]string{"apiKey": "old-secret"}, calls[0].data)
			assert.Equal(t, map[string]string{"apiKey": "new-secret"}, calls[1].data)
			assert.Equal(t, decodeProblem(t, w1)["secret_path"], decodeProblem(t, w2)["secret_path"])
			for _, leaked := range []string{"old-secret", "new-secret"} {
				assert.NotContains(t, w1.Body.String(), leaked)
				assert.NotContains(t, w2.Body.String(), leaked)
			}
		})
	}
}

func TestWriteConnectorCredential_NoSecretsClient_Returns503(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
	}{
		{name: "secrets client not configured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "unwritten-secret"}

			w := do(credentialWriteRealHandler(nil), adminReq(http.MethodPost, credentialWritePath, body))

			require.Equal(t, http.StatusServiceUnavailable, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "UPSTREAM_UNAVAILABLE", prob["code"])
			assert.NotContains(t, w.Body.String(), "OpenBao")
			assert.NotContains(t, w.Body.String(), "unwritten-secret")
		})
	}
}

func TestWriteConnectorCredential_OpenBaoUnreachable_Returns503(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		addr  func(t *testing.T) string
		limit time.Duration
	}{
		{
			name:  "connection refused",
			addr:  func(*testing.T) string { return "http://127.0.0.1:1" },
			limit: 2 * time.Second,
		},
		{
			name: "no answer before the client timeout",
			addr: func(t *testing.T) string {
				release := make(chan struct{})
				srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					select {
					case <-release:
					case <-r.Context().Done():
					}
				}))
				t.Cleanup(func() {
					close(release)
					srv.Close()
				})
				return srv.URL
			},
			limit: 50 * time.Millisecond,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			addr := tc.addr(t)
			client := outboundhttp.NewSecretsClient(addr, "token", "secret", tc.limit)
			body := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "unwritten-secret"}

			w := do(credentialWriteRealHandler(client), adminReq(http.MethodPost, credentialWritePath, body))

			require.Equal(t, http.StatusServiceUnavailable, w.Code)
			assert.Equal(t, "UPSTREAM_UNAVAILABLE", decodeProblem(t, w)["code"])
			assert.NotContains(t, w.Body.String(), strings.TrimPrefix(addr, "http://"))
			assert.NotContains(t, w.Body.String(), "unwritten-secret")
		})
	}
}

func TestWriteConnectorCredential_OpenBaoRequestCannotBeBuilt_Returns500(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr string
	}{
		{name: "address with a control character", addr: "http://open\x7fbao.invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := outboundhttp.NewSecretsClient(tc.addr, "token", "secret", time.Second)
			body := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "unwritten-secret"}

			w := do(credentialWriteRealHandler(client), adminReq(http.MethodPost, credentialWritePath, body))

			require.Equal(t, http.StatusInternalServerError, w.Code)
			assert.Equal(t, "INTERNAL_ERROR", decodeProblem(t, w)["code"])
			assert.NotContains(t, w.Body.String(), "bao.invalid")
			assert.NotContains(t, w.Body.String(), "invalid control character")
			assert.NotContains(t, w.Body.String(), "unwritten-secret")
		})
	}
}
