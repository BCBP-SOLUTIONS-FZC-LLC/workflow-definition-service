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

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/v2/pkg/registry"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

const (
	w6bAliasPath       = "/api/v1/internal/connector-aliases/rest"
	w6bEventsPath      = "/api/v1/internal/events"
	w6bProblemBadReq   = "https://api.workflow.platform/errors/bad-request"
	w6bProblemInvalid  = "https://api.workflow.platform/errors/validation-failed"
	w6bProblemInternal = "https://api.workflow.platform/errors/internal-error"
)

type w6bParam struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type w6bProblem struct {
	Type          string     `json:"type"`
	Title         string     `json:"title"`
	Status        int        `json:"status"`
	Detail        string     `json:"detail"`
	Instance      string     `json:"instance"`
	Code          string     `json:"code"`
	InvalidParams []w6bParam `json:"invalid_params"`
}

func w6bDecodeProblem(t *testing.T, w *httptest.ResponseRecorder) w6bProblem {
	t.Helper()
	require.Contains(t, w.Header().Get("Content-Type"), "application/problem+json")
	var p w6bProblem
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p), w.Body.String())
	assert.Equal(t, w.Code, p.Status)
	return p
}

func w6bRawReq(method, path, body string, admin bool) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", testTenantID.String())
	r.Header.Set("x-user-id", testUserID.String())
	if admin {
		r.Header.Set("x-tenant-roles", "tenant_admin")
	}
	return r
}

func w6bRegisterMetrics(t *testing.T) {
	t.Helper()
	log, err := logger.NewLogger("dev")
	require.NoError(t, err)
	_ = gincommon.ObservabilityMiddlewares(gincommon.Config{
		Logger: log, ServiceName: "workflow-definition-service", BuildVersion: "test",
		Domain: observability.Domain, Environment: "test",
	})
	observability.Register()
}

type w6bLogEntry struct {
	msg    string
	fields map[string]any
}

type w6bCapLogger struct {
	mu     sync.Mutex
	warns  []w6bLogEntry
	errors []w6bLogEntry
}

func (l *w6bCapLogger) Debug(string, map[string]any) { /* not asserted */ }
func (l *w6bCapLogger) Info(string, map[string]any)  { /* not asserted */ }
func (l *w6bCapLogger) Fatal(string, map[string]any) { /* not asserted */ }

func (l *w6bCapLogger) Warn(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, w6bLogEntry{msg: msg, fields: fields})
}

func (l *w6bCapLogger) Error(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, w6bLogEntry{msg: msg, fields: fields})
}

var _ port.Logger = (*w6bCapLogger)(nil)

type w6bConnectorFake struct {
	mu sync.Mutex

	aliasErr   error
	aliasCalls []domain.ConnectorRestAlias

	credErr   error
	credCalls []w6bCredentialCall
}

type w6bCredentialCall struct {
	tenantID, userID uuid.UUID
	connectorType    string
	fieldName        string
	value            string
}

func (f *w6bConnectorFake) Registry(context.Context) map[string]registry.Definition { return nil }

func (f *w6bConnectorFake) WriteCredential(_ context.Context, tenantID, userID uuid.UUID, connectorType, fieldName, value string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.credCalls = append(f.credCalls, w6bCredentialCall{tenantID: tenantID, userID: userID, connectorType: connectorType, fieldName: fieldName, value: value})
	return "", f.credErr
}

func (f *w6bConnectorFake) RevokeCredential(context.Context, uuid.UUID, uuid.UUID, string, string) error {
	return nil
}

func (f *w6bConnectorFake) ListAliases(context.Context) ([]domain.ConnectorRestAlias, error) {
	return nil, nil
}

func (f *w6bConnectorFake) WriteRestAlias(_ context.Context, a domain.ConnectorRestAlias) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aliasCalls = append(f.aliasCalls, a)
	return f.aliasErr
}

func (f *w6bConnectorFake) DeleteRestAlias(context.Context, string) (bool, error) { return true, nil }

func w6bInternalRouter(h *handler.Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	httpadapter.RegisterInternalRoutes(r.Group("/api/v1/internal"), h)
	return r
}

func w6bValidAliasBody() map[string]any {
	return map[string]any{
		"alias":        "tender-get",
		"method":       "GET",
		"baseURL":      "http://tender-service.internal",
		"pathTemplate": "/tenders/{id}",
	}
}

func w6bRequiredFieldCases(fields ...string) []struct {
	name  string
	field string
	apply func(map[string]any)
} {
	var cases []struct {
		name  string
		field string
		apply func(map[string]any)
	}
	for _, f := range fields {
		cases = append(cases,
			struct {
				name  string
				field string
				apply func(map[string]any)
			}{f + " absent", f, func(b map[string]any) { delete(b, f) }},
			struct {
				name  string
				field string
				apply func(map[string]any)
			}{f + " empty string", f, func(b map[string]any) { b[f] = "" }},
		)
	}
	return cases
}

func w6bAssertAliasFieldRefused(t *testing.T, field string, apply func(map[string]any)) {
	t.Helper()
	conn := &w6bConnectorFake{}
	body := w6bValidAliasBody()
	apply(body)
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	w := do(w6bInternalRouter(handler.New(handler.Services{Connectors: conn})), w6bRawReq(http.MethodPost, w6bAliasPath, string(raw), false))

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	p := w6bDecodeProblem(t, w)
	assert.Equal(t, "BAD_REQUEST", p.Code)
	assert.Equal(t, w6bProblemBadReq, p.Type)
	assert.Equal(t, "request body has missing or invalid fields", p.Detail)
	assert.Equal(t, w6bAliasPath, p.Instance)
	assert.Equal(t, []w6bParam{{Name: field, Reason: "is required"}}, p.InvalidParams)
	assert.Empty(t, conn.aliasCalls, "a body missing %s must not reach the service", field)
}

func TestWriteConnectorRestAlias_MissingOrEmptyAlias_Returns400NamingAlias(t *testing.T) {
	for _, tc := range w6bRequiredFieldCases("alias") {
		t.Run(tc.name, func(t *testing.T) {
			w6bAssertAliasFieldRefused(t, tc.field, tc.apply)
		})
	}
}

func TestWriteConnectorRestAlias_MissingOrEmptyRequestField_Returns400NamingThatField(t *testing.T) {
	for _, tc := range w6bRequiredFieldCases("method", "baseURL", "pathTemplate") {
		t.Run(tc.name, func(t *testing.T) {
			w6bAssertAliasFieldRefused(t, tc.field, tc.apply)
		})
	}
}

func TestWriteConnectorRestAlias_InvalidAliasInput_Returns400InvalidInputWithErrorText(t *testing.T) {
	cases := []struct {
		name       string
		body       map[string]any
		serviceErr error
		wantDetail string
	}{
		{
			name:       "a wrapped sentinel from the service",
			body:       w6bValidAliasBody(),
			serviceErr: fmt.Errorf("%w: base URL host is not allowed", domain.ErrInvalidConnectorAliasInput),
			wantDetail: "invalid connector alias input: base URL host is not allowed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorIs(t, tc.serviceErr, domain.ErrInvalidConnectorAliasInput)
			conn := &w6bConnectorFake{aliasErr: tc.serviceErr}
			raw, err := json.Marshal(tc.body)
			require.NoError(t, err)

			w := do(w6bInternalRouter(handler.New(handler.Services{Connectors: conn})), w6bRawReq(http.MethodPost, w6bAliasPath, string(raw), false))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			p := w6bDecodeProblem(t, w)
			assert.Equal(t, "INVALID_INPUT", p.Code)
			assert.Equal(t, w6bProblemBadReq, p.Type)
			assert.Equal(t, tc.wantDetail, p.Detail)
			assert.Empty(t, p.InvalidParams)
			require.Len(t, conn.aliasCalls, 1)
			assert.Equal(t, tc.body["alias"], conn.aliasCalls[0].Alias)
			assert.Equal(t, tc.body["method"], conn.aliasCalls[0].Method)
		})
	}
}

func TestWriteConnectorCredential_InvalidCredentialInput_Returns400InvalidInputWithErrorText(t *testing.T) {
	conn := &w6bConnectorFake{credErr: fmt.Errorf("%w: field_name is not an input of connector storage", domain.ErrInvalidConnectorCredentialInput)}
	h := handler.New(handler.Services{Connectors: conn})
	body := `{"connector_type":"storage","field_name":"bogus","value":"hunter2-secret"}`

	w := do(newRouter(h), w6bRawReq(http.MethodPost, "/api/v1/connectors/credentials", body, true))

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	p := w6bDecodeProblem(t, w)
	assert.Equal(t, "INVALID_INPUT", p.Code)
	assert.Equal(t, w6bProblemBadReq, p.Type)
	assert.Equal(t, "invalid connector credential input: field_name is not an input of connector storage", p.Detail)
	assert.Empty(t, p.InvalidParams)
	assert.NotContains(t, w.Body.String(), "hunter2-secret")
	require.Len(t, conn.credCalls, 1)
	assert.Equal(t, w6bCredentialCall{tenantID: testTenantID, userID: testUserID, connectorType: "storage", fieldName: "bogus", value: "hunter2-secret"}, conn.credCalls[0])
}

func TestInternalEvents_BodyThatIsNotAnEnvelope_Returns400AndCountsBadPayload(t *testing.T) {
	w6bRegisterMetrics(t)
	cases := []struct {
		name string
		body string
	}{
		{"not json", `not-json`},
		{"truncated json", `{"id":"x","type":`},
		{"an array instead of an object", `[1,2]`},
		{"a field of the wrong type", `{"id":5}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rev := &fakeMembershipRevoker{}
			h := handler.New(handler.Services{Membership: rev})
			counter := observability.InternalEventsIngestTotal.WithLabelValues("unknown", "bad_payload")
			before := testutil.ToFloat64(counter)

			w := do(w6bInternalRouter(h), w6bRawReq(http.MethodPost, w6bEventsPath, tc.body, false))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			p := w6bDecodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", p.Code)
			assert.Equal(t, w6bProblemBadReq, p.Type)
			assert.Equal(t, "invalid event envelope", p.Detail)
			assert.Equal(t, w6bEventsPath, p.Instance)
			assert.Equal(t, before+1, testutil.ToFloat64(counter), "one bad_payload under the unknown event type")
			assert.False(t, rev.called || rev.tenantCalled, "the service must not be called")
		})
	}
}

type w6bCreatedText struct {
	key, name, description string
}

func w6bWorkflowHandler(log port.Logger) (*handler.Handler, *int) {
	h, calls, _ := w6bCapturingWorkflowHandler(log)
	return h, calls
}

func w6bCapturingWorkflowHandler(log port.Logger) (*handler.Handler, *int, *[]w6bCreatedText) {
	calls := new(int)
	created := new([]w6bCreatedText)
	wf := &fakeWorkflowSvc{
		create: func(_ context.Context, _, _ uuid.UUID, key, name, description, _ string) (*domain.Workflow, *domain.WorkflowVersion, error) {
			*calls++
			*created = append(*created, w6bCreatedText{key: key, name: name, description: description})
			return newWorkflow(), newDraftVersion(), nil
		},
	}
	return handler.New(handler.Services{Workflows: wf, Log: log}), calls, created
}

func TestCreateWorkflow_FieldOfTheWrongType_NamesFieldAndSaysWrongType(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
	}{
		{"key as a number", `{"key":5,"name":"n","bpmn_xml":"x"}`, "key"},
		{"name as an object", `{"key":"k","name":{"a":"b"},"bpmn_xml":"x"}`, "name"},
		{"bpmn_xml as a list", `{"key":"k","name":"n","bpmn_xml":["x"]}`, "bpmn_xml"},
		{"description as a boolean", `{"key":"k","name":"n","bpmn_xml":"x","description":true}`, "description"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, calls := w6bWorkflowHandler(nil)

			w := do(newRouter(h), w6bRawReq(http.MethodPost, "/api/v1/workflows", tc.body, true))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			p := w6bDecodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", p.Code)
			assert.Equal(t, w6bProblemBadReq, p.Type)
			assert.Equal(t, "request body has a field of the wrong type", p.Detail)
			assert.Equal(t, []w6bParam{{Name: tc.field, Reason: "has the wrong type"}}, p.InvalidParams)
			assert.Zero(t, *calls)
		})
	}
}

func TestCreateWorkflow_MissingOrEmptyRequiredFields_ReasonIsRequired(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"every field absent", `{}`, []string{"key", "name", "bpmn_xml"}},
		{"every field an empty string", `{"key":"","name":"","bpmn_xml":""}`, []string{"key", "name", "bpmn_xml"}},
		{"only name empty", `{"key":"k","name":"","bpmn_xml":"x"}`, []string{"name"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, calls := w6bWorkflowHandler(nil)

			w := do(newRouter(h), w6bRawReq(http.MethodPost, "/api/v1/workflows", tc.body, true))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			p := w6bDecodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", p.Code)
			assert.Equal(t, "request body has missing or invalid fields", p.Detail)
			var names []string
			for _, param := range p.InvalidParams {
				names = append(names, param.Name)
				assert.Equal(t, "is required", param.Reason, param.Name)
			}
			assert.ElementsMatch(t, tc.want, names)
			assert.Zero(t, *calls)
		})
	}
}

func TestCreateWorkflow_TextOverItsLimitCountedInRunes_Returns422(t *testing.T) {
	two := strings.Repeat("é", 255)
	emoji := strings.Repeat("😀", 2000)
	cases := []struct {
		name       string
		key        string
		wfName     string
		desc       string
		wantStatus int
		want       []w6bParam
	}{
		{name: "255 two-byte runes in name are within the limit", key: "k", wfName: two, desc: "", wantStatus: http.StatusCreated},
		{name: "2000 four-byte runes in description are within the limit", key: "k", wfName: "n", desc: emoji, wantStatus: http.StatusCreated},
		{
			name: "256 runes in name", key: "k", wfName: two + "é", desc: "", wantStatus: http.StatusUnprocessableEntity,
			want: []w6bParam{{Name: "name", Reason: "must not exceed 255 characters"}},
		},
		{
			name: "2001 runes in description", key: "k", wfName: "n", desc: emoji + "😀", wantStatus: http.StatusUnprocessableEntity,
			want: []w6bParam{{Name: "description", Reason: "must not exceed 2000 characters"}},
		},
		{
			name: "256 runes in key", key: strings.Repeat("k", 256), wfName: "n", desc: "", wantStatus: http.StatusUnprocessableEntity,
			want: []w6bParam{{Name: "key", Reason: "must not exceed 255 characters"}},
		},
		{
			name: "key and description both over, reported in field order", key: strings.Repeat("é", 256), wfName: "n", desc: emoji + "😀",
			wantStatus: http.StatusUnprocessableEntity,
			want: []w6bParam{
				{Name: "key", Reason: "must not exceed 255 characters"},
				{Name: "description", Reason: "must not exceed 2000 characters"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, calls, created := w6bCapturingWorkflowHandler(nil)
			raw, err := json.Marshal(map[string]string{"key": tc.key, "name": tc.wfName, "description": tc.desc, "bpmn_xml": "<definitions/>"})
			require.NoError(t, err)

			w := do(newRouter(h), w6bRawReq(http.MethodPost, "/api/v1/workflows", string(raw), true))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			if tc.wantStatus == http.StatusCreated {
				assert.Equal(t, 1, *calls)
				require.Len(t, *created, 1)
				assert.Equal(t, w6bCreatedText{key: tc.key, name: tc.wfName, description: tc.desc}, (*created)[0], "the text reaches the service unchanged")
				return
			}
			assert.Zero(t, *calls, "an over-long field must not reach the service")
			p := w6bDecodeProblem(t, w)
			assert.Equal(t, "INVALID_INPUT", p.Code)
			assert.Equal(t, w6bProblemInvalid, p.Type)
			assert.Equal(t, "request fields exceed maximum length", p.Detail)
			assert.Equal(t, tc.want, p.InvalidParams)
		})
	}
}

func TestGetWorkflow_VersionsLimitOutsideRangeOrNotAnInteger_Returns400InvalidInput(t *testing.T) {
	for _, query := range []string{"-1", "0", "101", "99999", "abc", "1.5"} {
		t.Run(query, func(t *testing.T) {
			called := false
			wf := &fakeWorkflowSvc{get: func(context.Context, uuid.UUID, uuid.UUID, int) (*domain.Workflow, []*domain.WorkflowVersion, error) {
				called = true
				return newWorkflow(), nil, nil
			}}
			h := newHandler(wf, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"?versions_limit="+query, nil))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			p := w6bDecodeProblem(t, w)
			assert.Equal(t, "INVALID_INPUT", p.Code)
			assert.Equal(t, w6bProblemBadReq, p.Type)
			assert.Equal(t, "query parameters are invalid", p.Detail)
			assert.Equal(t, []w6bParam{{Name: "versions_limit", Reason: "must be an integer between 1 and 100"}}, p.InvalidParams)
			assert.False(t, called, "a bad versions_limit must not reach the service")
		})
	}
}

func TestGetWorkflow_VersionsLimitAtTheBounds_ReachesServiceWithCallerIDs(t *testing.T) {
	for _, limit := range []int{1, 100} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			var gotTenant, gotWF uuid.UUID
			var gotLimit int
			wf := &fakeWorkflowSvc{get: func(_ context.Context, tenantID, id uuid.UUID, versionsLimit int) (*domain.Workflow, []*domain.WorkflowVersion, error) {
				gotTenant, gotWF, gotLimit = tenantID, id, versionsLimit
				return newWorkflow(), []*domain.WorkflowVersion{}, nil
			}}
			h := newHandler(wf, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, fmt.Sprintf("/api/v1/workflows/%s?versions_limit=%d", testWFID, limit), nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, limit, gotLimit)
			assert.Equal(t, testTenantID, gotTenant)
			assert.Equal(t, testWFID, gotWF)
		})
	}
}

func TestListWorkflows_NoIdentityInContext_Returns500MissingRequestContext(t *testing.T) {
	called := false
	wf := &fakeWorkflowSvc{list: func(context.Context, uuid.UUID, port.WorkflowFilter) ([]*domain.Workflow, int64, error) {
		called = true
		return nil, 0, nil
	}}
	h := newHandler(wf, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newBareRouter(h), req(http.MethodGet, "/api/v1/workflows", nil))

	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	p := w6bDecodeProblem(t, w)
	assert.Equal(t, "INTERNAL_ERROR", p.Code)
	assert.Equal(t, w6bProblemInternal, p.Type)
	assert.Equal(t, "missing request context", p.Detail)
	assert.Equal(t, "/api/v1/workflows", p.Instance)
	assert.False(t, called, "the list must not run without an identity")
}

func TestListWorkflows_ServiceFailure_LogsRequestFieldsOnlyFor5xx(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantLogged bool
	}{
		{"unexpected error", errors.New("db exploded: password=hunter2"), http.StatusInternalServerError, "INTERNAL_ERROR", true},
		{"upstream unavailable", domain.ErrUpstreamUnavailable, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", true},
		{"client error", domain.ErrForbidden, http.StatusForbidden, "FORBIDDEN", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := &w6bCapLogger{}
			var gotTenant uuid.UUID
			wf := &fakeWorkflowSvc{list: func(_ context.Context, tenantID uuid.UUID, _ port.WorkflowFilter) ([]*domain.Workflow, int64, error) {
				gotTenant = tenantID
				return nil, 0, tc.err
			}}
			h := handler.New(handler.Services{Workflows: wf, Log: log})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows", nil))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			assert.Equal(t, testTenantID, gotTenant)
			assert.Equal(t, tc.wantCode, w6bDecodeProblem(t, w).Code)
			assert.NotContains(t, w.Body.String(), "hunter2")
			if !tc.wantLogged {
				assert.Empty(t, log.errors)
				return
			}
			require.Len(t, log.errors, 1)
			entry := log.errors[0]
			assert.Equal(t, "request failed", entry.msg)
			assert.Equal(t, tc.wantStatus, entry.fields["status"])
			assert.Equal(t, tc.wantCode, entry.fields["code"])
			assert.Equal(t, tc.err.Error(), entry.fields["error"])
			assert.Equal(t, testTenantID.String(), entry.fields["tenant_id"])
			assert.Equal(t, testUserID.String(), entry.fields["user_id"])
			assert.Equal(t, "/api/v1/workflows", entry.fields["path"])
			assert.Equal(t, "", entry.fields["trace_id"], "no span is active, so the trace id is empty")
		})
	}
}

func TestCreateWorkflow_RefusedBody_LogsWarningWithRoutePatternAndRawErrorOnly(t *testing.T) {
	updateDraft := &fakeDraftSvc{}
	cases := []struct {
		name        string
		method      string
		path        string
		body        string
		wantPath    string
		wantErrLike string
		wantDetail  string
		wantParams  []w6bParam
	}{
		{
			name: "truncated json on a fixed route", method: http.MethodPost, path: "/api/v1/workflows", body: `{"key": `,
			wantPath: "/api/v1/workflows", wantErrLike: "decode request body: unexpected EOF", wantDetail: "request body is not valid JSON",
		},
		{
			name: "truncated json on a route with a parameter", method: http.MethodPut, path: "/api/v1/workflows/" + testWFID.String() + "/draft", body: `{"name": `,
			wantPath: "/api/v1/workflows/:id/draft", wantErrLike: "decode request body: unexpected EOF", wantDetail: "request body is not valid JSON",
		},
		{
			name: "a field of the wrong type", method: http.MethodPost, path: "/api/v1/workflows", body: `{"key":5,"name":"n","bpmn_xml":"x"}`,
			wantPath: "/api/v1/workflows", wantErrLike: "cannot unmarshal number", wantDetail: "request body has a field of the wrong type",
			wantParams: []w6bParam{{Name: "key", Reason: "has the wrong type"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := &w6bCapLogger{}
			h := handler.New(handler.Services{Workflows: &fakeWorkflowSvc{}, Drafts: updateDraft, Log: log})

			w := do(newRouter(h), w6bRawReq(tc.method, tc.path, tc.body, true))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			p := w6bDecodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", p.Code)
			assert.Equal(t, tc.wantDetail, p.Detail)
			assert.Equal(t, tc.wantParams, p.InvalidParams)
			assert.NotContains(t, w.Body.String(), tc.wantErrLike, "the raw error is logged, not returned")
			require.Len(t, log.warns, 1)
			assert.Equal(t, "invalid request body", log.warns[0].msg)
			assert.Equal(t, tc.wantPath, log.warns[0].fields["path"])
			errText, _ := log.warns[0].fields["error"].(string)
			assert.Contains(t, errText, tc.wantErrLike)
		})
	}
}

func TestCreateWorkflow_RefusedBodyWithoutLogger_StillAnswers400(t *testing.T) {
	h, calls := w6bWorkflowHandler(nil)

	w := do(newRouter(h), w6bRawReq(http.MethodPost, "/api/v1/workflows", `{"key": `, true))

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	p := w6bDecodeProblem(t, w)
	assert.Equal(t, "BAD_REQUEST", p.Code)
	assert.Equal(t, "request body is not valid JSON", p.Detail)
	assert.Zero(t, *calls)
}

func TestCreateWorkflow_ManyOrLongUnknownFields_AreCappedAndTruncatedInTheProblem(t *testing.T) {
	t.Run("thirty unknown fields report the first twenty by name", func(t *testing.T) {
		h, calls := w6bWorkflowHandler(nil)
		fields := []string{`"key":"k"`, `"name":"n"`, `"bpmn_xml":"x"`}
		var want []string
		for i := range 30 {
			fields = append(fields, fmt.Sprintf(`"extra%02d":1`, i))
			if i < 20 {
				want = append(want, fmt.Sprintf("extra%02d", i))
			}
		}

		w := do(newRouter(h), w6bRawReq(http.MethodPost, "/api/v1/workflows", "{"+strings.Join(fields, ",")+"}", true))

		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		p := w6bDecodeProblem(t, w)
		assert.Equal(t, "request body has fields this endpoint does not define", p.Detail)
		var got []string
		for _, param := range p.InvalidParams {
			got = append(got, param.Name)
			assert.Equal(t, "is not a known field", param.Reason)
		}
		assert.Equal(t, want, got)
		assert.Zero(t, *calls)
	})

	t.Run("an unknown field name over 64 characters is cut to 64", func(t *testing.T) {
		h, calls := w6bWorkflowHandler(nil)
		long := strings.Repeat("é", 90)
		body := fmt.Sprintf(`{"key":"k","name":"n","bpmn_xml":"x",%q:1}`, long)

		w := do(newRouter(h), w6bRawReq(http.MethodPost, "/api/v1/workflows", body, true))

		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		p := w6bDecodeProblem(t, w)
		require.Len(t, p.InvalidParams, 1)
		assert.Equal(t, strings.Repeat("é", 64), p.InvalidParams[0].Name)
		assert.Equal(t, "is not a known field", p.InvalidParams[0].Reason)
		assert.Zero(t, *calls)
	})
}
