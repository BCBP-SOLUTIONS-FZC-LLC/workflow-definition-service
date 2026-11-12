package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const (
	w8bCredentialPath        = "/api/v1/connectors/credentials"
	w8bBadRequestProblemType = "https://api.workflow.platform/errors/bad-request"
	w8bUnavailableType       = "https://api.workflow.platform/errors/service-unavailable"
	w8bInternalType          = "https://api.workflow.platform/errors/internal-error"
)

type w8bCredentialCall struct {
	tenantID      uuid.UUID
	userID        uuid.UUID
	connectorType string
	fieldName     string
	value         string
}

type w8bCredentialSvc struct {
	fakeConnectorSvc
	mu         sync.Mutex
	recorded   []w8bCredentialCall
	secretPath string
	err        error
}

func (s *w8bCredentialSvc) WriteCredential(_ context.Context, tenantID, userID uuid.UUID, connectorType, fieldName, value string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recorded = append(s.recorded, w8bCredentialCall{tenantID, userID, connectorType, fieldName, value})
	return s.secretPath, s.err
}

func (s *w8bCredentialSvc) calls() []w8bCredentialCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]w8bCredentialCall(nil), s.recorded...)
}

type w8bLogEntry struct {
	msg    string
	fields map[string]any
}

type w8bLogger struct {
	mu     sync.Mutex
	errors []w8bLogEntry
}

func (l *w8bLogger) Debug(string, map[string]any) { /* no-op */ }
func (l *w8bLogger) Info(string, map[string]any)  { /* no-op */ }
func (l *w8bLogger) Warn(string, map[string]any)  { /* no-op */ }
func (l *w8bLogger) Fatal(string, map[string]any) { /* no-op */ }
func (l *w8bLogger) Error(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, w8bLogEntry{msg: msg, fields: fields})
}

func (l *w8bLogger) recordedErrors() []w8bLogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]w8bLogEntry(nil), l.errors...)
}

func w8bCredentialRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, w8bCredentialPath, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", testTenantID.String())
	r.Header.Set("x-user-id", testUserID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	return r
}

func w8bCredentialBody(t *testing.T, mutate func(m map[string]any)) string {
	t.Helper()
	m := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "sg-live-abc"}
	mutate(m)
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return string(b)
}

func w8bAssertProblem(t *testing.T, w *httptest.ResponseRecorder, status int, problemType, code string) map[string]any {
	t.Helper()
	require.Equal(t, status, w.Code, w.Body.String())
	assert.Contains(t, w.Header().Get("Content-Type"), "application/problem+json")
	prob := decodeProblem(t, w)
	assert.Equal(t, problemType, prob["type"])
	assert.Equal(t, code, prob["code"])
	assert.EqualValues(t, status, prob["status"])
	assert.Equal(t, w8bCredentialPath, prob["instance"])
	return prob
}

func w8bInvalidParams(t *testing.T, prob map[string]any) []map[string]any {
	t.Helper()
	raw, ok := prob["invalid_params"].([]any)
	require.True(t, ok, "problem carries invalid_params: %v", prob)
	out := make([]map[string]any, len(raw))
	for i, p := range raw {
		m, isMap := p.(map[string]any)
		require.True(t, isMap)
		out[i] = m
	}
	return out
}

func TestWriteConnectorCredentialW8b_RequiredFieldMissingNullOrEmpty_Returns400NamingTheField(t *testing.T) {
	t.Parallel()

	modes := []struct {
		name   string
		mutate func(m map[string]any, field string)
	}{
		{"missing", func(m map[string]any, field string) { delete(m, field) }},
		{"null", func(m map[string]any, field string) { m[field] = nil }},
		{"empty string", func(m map[string]any, field string) { m[field] = "" }},
	}
	type credentialCase struct {
		name  string
		field string
		mode  func(m map[string]any, field string)
	}
	var cases []credentialCase
	for _, field := range []string{"connector_type", "field_name", "value"} {
		for _, mode := range modes {
			cases = append(cases, credentialCase{name: field + " " + mode.name, field: field, mode: mode.mutate})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &w8bCredentialSvc{}
			log := &w8bLogger{}
			router := newRouter(handler.New(handler.Services{Connectors: svc, Log: log}))
			body := w8bCredentialBody(t, func(m map[string]any) { tc.mode(m, tc.field) })

			w := do(router, w8bCredentialRequest(body))

			prob := w8bAssertProblem(t, w, http.StatusBadRequest, w8bBadRequestProblemType, "BAD_REQUEST")
			assert.Equal(t, "request body has missing or invalid fields", prob["detail"])
			params := w8bInvalidParams(t, prob)
			require.Len(t, params, 1)
			assert.Equal(t, tc.field, params[0]["name"])
			assert.Equal(t, "is required", params[0]["reason"])
			assert.Empty(t, svc.calls(), "a body with a required field missing must not reach the service")
			assert.Empty(t, log.recordedErrors())
		})
	}
}

func TestWriteConnectorCredentialW8b_SeveralRequiredFieldsAbsent_ListsEachInFieldOrder(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		body      string
		wantNames []string
	}{
		{"field_name and value absent", `{"connector_type":"send-email"}`, []string{"field_name", "value"}},
		{"connector_type and value empty", `{"connector_type":"","field_name":"apiKey","value":""}`, []string{"connector_type", "value"}},
		{"empty object", `{}`, []string{"connector_type", "field_name", "value"}},
		{"all three null", `{"connector_type":null,"field_name":null,"value":null}`, []string{"connector_type", "field_name", "value"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &w8bCredentialSvc{}
			router := newRouter(handler.New(handler.Services{Connectors: svc}))

			w := do(router, w8bCredentialRequest(tc.body))

			prob := w8bAssertProblem(t, w, http.StatusBadRequest, w8bBadRequestProblemType, "BAD_REQUEST")
			params := w8bInvalidParams(t, prob)
			require.Len(t, params, len(tc.wantNames))
			for i, name := range tc.wantNames {
				assert.Equal(t, name, params[i]["name"])
				assert.Equal(t, "is required", params[i]["reason"])
			}
			assert.Empty(t, svc.calls())
		})
	}
}

func TestWriteConnectorCredentialW8b_NonStringField_Returns400NamingTheField(t *testing.T) {
	t.Parallel()

	values := []struct {
		name  string
		value any
	}{
		{"number", 123},
		{"boolean", true},
		{"array", []any{"x"}},
		{"object", map[string]any{"a": 1}},
	}
	type credentialCase struct {
		name  string
		field string
		value any
	}
	var cases []credentialCase
	for _, field := range []string{"connector_type", "field_name", "value"} {
		for _, v := range values {
			cases = append(cases, credentialCase{name: field + " as " + v.name, field: field, value: v.value})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &w8bCredentialSvc{}
			router := newRouter(handler.New(handler.Services{Connectors: svc}))
			body := w8bCredentialBody(t, func(m map[string]any) { m[tc.field] = tc.value })

			w := do(router, w8bCredentialRequest(body))

			prob := w8bAssertProblem(t, w, http.StatusBadRequest, w8bBadRequestProblemType, "BAD_REQUEST")
			assert.Equal(t, "request body has a field of the wrong type", prob["detail"])
			params := w8bInvalidParams(t, prob)
			require.Len(t, params, 1)
			assert.Equal(t, tc.field, params[0]["name"])
			assert.Equal(t, "has the wrong type", params[0]["reason"])
			assert.Empty(t, svc.calls())
		})
	}
}

func TestWriteConnectorCredentialW8b_ExtraBodyField_Returns400NamingTheExtraField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		extra string
		value any
	}{
		{"extra string member", "zzq_unknown", "typo"},
		{"extra member with a null value", "zzq_unknown", nil},
		{"member that differs from a known name by a suffix", "value_zzq", "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &w8bCredentialSvc{}
			router := newRouter(handler.New(handler.Services{Connectors: svc}))
			body := w8bCredentialBody(t, func(m map[string]any) { m[tc.extra] = tc.value })

			w := do(router, w8bCredentialRequest(body))

			prob := w8bAssertProblem(t, w, http.StatusBadRequest, w8bBadRequestProblemType, "BAD_REQUEST")
			assert.Equal(t, "request body has fields this endpoint does not define", prob["detail"])
			params := w8bInvalidParams(t, prob)
			require.Len(t, params, 1)
			assert.Equal(t, tc.extra, params[0]["name"])
			assert.Equal(t, "is not a known field", params[0]["reason"])
			assert.Empty(t, svc.calls(), "a body with an unknown member must not reach the service")
		})
	}
}

func TestWriteConnectorCredentialW8b_Success_AnswersExactlyTheSecretPathAndPassesTheCallerOn(t *testing.T) {
	t.Parallel()

	secretPath := fmt.Sprintf("connectors/%s/send-email/apiKey", testTenantID)
	svc := &w8bCredentialSvc{secretPath: secretPath}
	router := newRouter(handler.New(handler.Services{Connectors: svc}))
	body := w8bCredentialBody(t, func(map[string]any) {})

	w := do(router, w8bCredentialRequest(body))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, map[string]any{"secret_path": secretPath}, resp, "the body carries secret_path and nothing else")
	assert.NotContains(t, w.Body.String(), "sg-live-abc")
	calls := svc.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, w8bCredentialCall{
		tenantID:      testTenantID,
		userID:        testUserID,
		connectorType: "send-email",
		fieldName:     "apiKey",
		value:         "sg-live-abc",
	}, calls[0])
}

func TestWriteConnectorCredentialW8b_ServiceServerError_AnswersAProblemAndLogsOnce(t *testing.T) {
	t.Parallel()

	const rawCause = "dial tcp 10.0.0.9:8200: connection refused"
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantType   string
		wantCode   string
		wantDetail string
	}{
		{
			name:       "secrets store unavailable",
			err:        fmt.Errorf("write credential: %w: %s", domain.ErrUpstreamUnavailable, rawCause),
			wantStatus: http.StatusServiceUnavailable,
			wantType:   w8bUnavailableType,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantDetail: "an upstream dependency is temporarily unavailable",
		},
		{
			name:       "unclassified failure",
			err:        errors.New(rawCause),
			wantStatus: http.StatusInternalServerError,
			wantType:   w8bInternalType,
			wantCode:   "INTERNAL_ERROR",
			wantDetail: "an unexpected error occurred",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &w8bCredentialSvc{err: tc.err}
			log := &w8bLogger{}
			router := newRouter(handler.New(handler.Services{Connectors: svc, Log: log}))
			body := w8bCredentialBody(t, func(map[string]any) {})

			w := do(router, w8bCredentialRequest(body))

			prob := w8bAssertProblem(t, w, tc.wantStatus, tc.wantType, tc.wantCode)
			assert.Equal(t, tc.wantDetail, prob["detail"])
			assert.NotContains(t, w.Body.String(), "10.0.0.9")
			assert.NotContains(t, w.Body.String(), "sg-live-abc")

			entries := log.recordedErrors()
			require.Len(t, entries, 1, "a server error is logged exactly once")
			assert.Equal(t, "request failed", entries[0].msg)
			assert.EqualValues(t, tc.wantStatus, entries[0].fields["status"])
			assert.Equal(t, tc.wantCode, entries[0].fields["code"])
			assert.Equal(t, testTenantID.String(), entries[0].fields["tenant_id"])
			assert.Equal(t, testUserID.String(), entries[0].fields["user_id"])
			assert.Equal(t, w8bCredentialPath, entries[0].fields["path"])
			assert.Contains(t, fmt.Sprint(entries[0].fields["error"]), rawCause, "the log keeps the cause the body hides")
			assert.NotContains(t, fmt.Sprint(entries[0].fields), "sg-live-abc", "the secret value is never logged")
		})
	}
}

func TestWriteConnectorCredentialW8b_ServiceClientError_AnswersAProblemWithoutAnErrorLog(t *testing.T) {
	t.Parallel()

	svc := &w8bCredentialSvc{err: fmt.Errorf("write credential: %w", domain.ErrInvalidConnectorCredentialInput)}
	log := &w8bLogger{}
	router := newRouter(handler.New(handler.Services{Connectors: svc, Log: log}))
	body := w8bCredentialBody(t, func(map[string]any) {})

	w := do(router, w8bCredentialRequest(body))

	prob := w8bAssertProblem(t, w, http.StatusBadRequest, w8bBadRequestProblemType, "INVALID_INPUT")
	assert.Equal(t, "write credential: invalid connector credential input", prob["detail"])
	assert.Empty(t, log.recordedErrors(), "a refusal caused by the caller is not an error log line")
}
