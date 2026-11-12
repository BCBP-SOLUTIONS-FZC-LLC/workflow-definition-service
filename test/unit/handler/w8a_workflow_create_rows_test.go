package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const (
	w8aCreatePath  = "/api/v1/workflows"
	w8aProblemBase = "https://api.workflow.platform/errors/"
	w8aCreateXML   = "<definitions/>"
)

type w8aLogEntry struct {
	level  string
	msg    string
	fields map[string]any
}

type w8aLogger struct {
	mu      sync.Mutex
	entries []w8aLogEntry
}

func (l *w8aLogger) record(level, msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, w8aLogEntry{level: level, msg: msg, fields: fields})
}

func (l *w8aLogger) Debug(msg string, f map[string]any) { l.record("debug", msg, f) }
func (l *w8aLogger) Info(msg string, f map[string]any)  { l.record("info", msg, f) }
func (l *w8aLogger) Warn(msg string, f map[string]any)  { l.record("warn", msg, f) }
func (l *w8aLogger) Error(msg string, f map[string]any) { l.record("error", msg, f) }
func (l *w8aLogger) Fatal(msg string, f map[string]any) { l.record("fatal", msg, f) }

func (l *w8aLogger) at(level string) []w8aLogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []w8aLogEntry
	for _, e := range l.entries {
		if e.level == level {
			out = append(out, e)
		}
	}
	return out
}

type w8aCreateCall struct {
	tenant, user       uuid.UUID
	key, name, bpmnXML string
}

type w8aCreateRecorder struct {
	mu    sync.Mutex
	calls []w8aCreateCall
	fn    func(w8aCreateCall) error
}

func (r *w8aCreateRecorder) service() *fakeWorkflowSvc {
	return &fakeWorkflowSvc{
		create: func(_ context.Context, tenantID, userID uuid.UUID, key, name, _, xml string) (*domain.Workflow, *domain.WorkflowVersion, error) {
			call := w8aCreateCall{tenant: tenantID, user: userID, key: key, name: name, bpmnXML: xml}
			r.mu.Lock()
			r.calls = append(r.calls, call)
			r.mu.Unlock()
			if r.fn != nil {
				if err := r.fn(call); err != nil {
					return nil, nil, err
				}
			}
			return newWorkflow(), newDraftVersion(), nil
		},
	}
}

func (r *w8aCreateRecorder) recorded() []w8aCreateCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]w8aCreateCall(nil), r.calls...)
}

func w8aCreateBody(key, name, xml string) map[string]any {
	return map[string]any{"key": key, "name": name, "bpmn_xml": xml}
}

func w8aRawCreateReq(body, roles string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, w8aCreatePath, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", testTenantID.String())
	r.Header.Set("x-user-id", testUserID.String())
	if roles != "" {
		r.Header.Set("x-tenant-roles", roles)
	}
	return r
}

func w8aProtectedOnlyRouter(h *handler.Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	registerRoutes(r, h)
	return r
}

func w8aProblem(t *testing.T, w *httptest.ResponseRecorder, status int, code, typeSuffix string) map[string]any {
	t.Helper()
	require.Equal(t, status, w.Code, w.Body.String())
	assert.True(t, strings.HasPrefix(w.Header().Get("Content-Type"), "application/problem+json"), "content type = %q", w.Header().Get("Content-Type"))
	prob := decodeProblem(t, w)
	assert.Equal(t, code, prob["code"])
	assert.Equal(t, w8aProblemBase+typeSuffix, prob["type"])
	assert.EqualValues(t, status, prob["status"])
	assert.Equal(t, w8aCreatePath, prob["instance"])
	return prob
}

func w8aStoredEntries(m *memCache) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for k := range m.items {
		keys = append(keys, k)
	}
	return keys
}

func w8aCacheRouter(rec *w8aCreateRecorder, cache *memCache) *gin.Engine {
	return newRouter(handler.New(handler.Services{
		Workflows:      rec.service(),
		Cache:          (*fakeCache)(nil).wrap(cache),
		IdempotencyTTL: time.Hour,
	}))
}

func TestW8aCreateWorkflow_CallerWithoutAnAdminRole_IsRefusedForbiddenBeforeAnythingRuns(t *testing.T) {
	cases := []struct {
		name  string
		roles string
	}{
		{name: "role member", roles: "member"},
		{name: "role tenant member", roles: "tenant_member"},
		{name: "platform operator only", roles: "platform_operator"},
		{name: "platform operator with a non-admin role", roles: "platform_operator,tenant_member"},
		{name: "no roles header", roles: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &w8aCreateRecorder{}
			cache := newMemCache()
			router := w8aCacheRouter(rec, cache)
			r := w8aRawCreateReq(w8aJSONString(t, w8aCreateBody("refused-wf", "Refused", w8aCreateXML)), tc.roles)
			r.Header.Set("Idempotency-Key", "refused-key")

			w := do(router, r)

			prob := w8aProblem(t, w, http.StatusForbidden, "FORBIDDEN", "forbidden")
			assert.Equal(t, "caller lacks tenant_admin/tenant_owner role", prob["detail"])
			assert.Empty(t, rec.recorded(), "the service is not reached")
			assert.Empty(t, w8aStoredEntries(cache), "a refused caller leaves nothing in the idempotency cache")
		})
	}
}

func w8aJSONString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestW8aCreateWorkflow_TenantOwner_CreatesWithTheCallersIds(t *testing.T) {
	cases := []struct {
		name  string
		roles string
	}{
		{name: "tenant owner alone", roles: "tenant_owner"},
		{name: "tenant owner among other roles", roles: "tenant_member,tenant_owner"},
		{name: "tenant owner with platform operator", roles: "platform_operator,tenant_owner"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &w8aCreateRecorder{}
			router := newRouter(newHandler(rec.service(), &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{}))

			w := do(router, w8aRawCreateReq(w8aJSONString(t, w8aCreateBody("owner-wf", "Owner workflow", w8aCreateXML)), tc.roles))

			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, testWFID.String(), resp["workflow_id"])
			assert.Equal(t, testVerID.String(), resp["version_id"])
			assert.Equal(t, "Workflow created", resp["message"])
			calls := rec.recorded()
			require.Len(t, calls, 1)
			assert.Equal(t, w8aCreateCall{
				tenant: testTenantID, user: testUserID, key: "owner-wf", name: "Owner workflow", bpmnXML: w8aCreateXML,
			}, calls[0])
		})
	}
}

func TestW8aCreateWorkflow_NoIdentityInContext_Is500MissingRequestContext(t *testing.T) {
	routers := []struct {
		name  string
		build func(*handler.Handler) *gin.Engine
	}{
		{name: "no middleware at all", build: newBareRouter},
		{name: "library middleware without the identity step", build: w8aProtectedOnlyRouter},
	}
	for _, tc := range routers {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &w8aCreateRecorder{}
			router := tc.build(newHandler(rec.service(), &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{}))
			r := w8aRawCreateReq(w8aJSONString(t, w8aCreateBody("no-ctx", "No context", w8aCreateXML)), "tenant_admin")

			w := do(router, r)

			prob := w8aProblem(t, w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal-error")
			assert.Equal(t, "missing request context", prob["detail"])
			assert.Empty(t, rec.recorded())
		})
	}
}

func TestW8aCreateWorkflow_ServiceError_WritesOnlyTheMappedProblem(t *testing.T) {
	forbiddenXML := fmt.Errorf("bpmn create: %w: %w: %v", domain.ErrMalformedBPMN, domain.ErrForbiddenXML, "doctype is not allowed")
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantType   string
		wantWarn   bool
		wantError  bool
	}{
		{name: "duplicate key", err: domain.ErrDuplicateTemplateKey, wantStatus: http.StatusConflict, wantCode: "DUPLICATE_TEMPLATE_KEY", wantType: "conflict"},
		{name: "upstream unavailable", err: domain.ErrUpstreamUnavailable, wantStatus: http.StatusServiceUnavailable, wantCode: "UPSTREAM_UNAVAILABLE", wantType: "service-unavailable", wantError: true},
		{name: "malformed document", err: domain.ErrMalformedBPMN, wantStatus: http.StatusBadRequest, wantCode: "INVALID_BPMN_XML", wantType: "bad-request"},
		{name: "forbidden construct in the document", err: forbiddenXML, wantStatus: http.StatusBadRequest, wantCode: "INVALID_BPMN_XML", wantType: "bad-request", wantWarn: true},
		{name: "error no case knows", err: errors.New("zzq_unknown failure"), wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_ERROR", wantType: "internal-error", wantError: true},
	}
	allowed := map[string]bool{"type": true, "title": true, "status": true, "detail": true, "instance": true, "code": true}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &w8aCreateRecorder{fn: func(w8aCreateCall) error { return tc.err }}
			log := &w8aLogger{}
			router := newRouter(handler.New(handler.Services{Workflows: rec.service(), Log: log}))
			r := w8aRawCreateReq(w8aJSONString(t, w8aCreateBody("failing-wf", "Failing", w8aCreateXML)), "tenant_admin")
			r.RemoteAddr = "203.0.113.9:4321"

			w := do(router, r)

			w8aProblem(t, w, tc.wantStatus, tc.wantCode, tc.wantType)
			dec := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
			var body map[string]any
			require.NoError(t, dec.Decode(&body))
			_, err := dec.Token()
			assert.ErrorIs(t, err, io.EOF, "the response holds one document and nothing after it")
			for k := range body {
				assert.True(t, allowed[k], "unexpected field %q in a failure body", k)
			}
			for _, success := range []string{"workflow_id", "version_id", "message", "version_number"} {
				assert.NotContains(t, body, success)
			}
			assert.NotContains(t, w.Body.String(), "zzq_unknown")
			assert.Len(t, rec.recorded(), 1)

			warns := log.at("warn")
			if tc.wantWarn {
				require.Len(t, warns, 1)
				assert.Equal(t, "forbidden XML construct rejected", warns[0].msg)
				assert.Equal(t, "203.0.113.9", warns[0].fields["client_ip"])
				assert.Equal(t, testTenantID.String(), warns[0].fields["tenant_id"])
			} else {
				assert.Empty(t, warns)
			}
			errs := log.at("error")
			if tc.wantError {
				require.Len(t, errs, 1)
				assert.Equal(t, testTenantID.String(), errs[0].fields["tenant_id"])
				assert.Equal(t, testUserID.String(), errs[0].fields["user_id"])
				assert.EqualValues(t, tc.wantStatus, errs[0].fields["status"])
			} else {
				assert.Empty(t, errs)
			}
		})
	}
}

func TestW8aCreateWorkflow_MissingOrEmptyRequiredField_Is400NamingThatField(t *testing.T) {
	full := func() map[string]any { return w8aCreateBody("required-wf", "Required", w8aCreateXML) }
	without := func(field string) map[string]any {
		b := full()
		delete(b, field)
		return b
	}
	empty := func(field string) map[string]any {
		b := full()
		b[field] = ""
		return b
	}
	cases := []struct {
		name  string
		body  map[string]any
		field string
	}{
		{name: "key missing", body: without("key"), field: "key"},
		{name: "key empty", body: empty("key"), field: "key"},
		{name: "name missing", body: without("name"), field: "name"},
		{name: "name empty", body: empty("name"), field: "name"},
		{name: "bpmn_xml missing", body: without("bpmn_xml"), field: "bpmn_xml"},
		{name: "bpmn_xml empty", body: empty("bpmn_xml"), field: "bpmn_xml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &w8aCreateRecorder{}
			router := newRouter(newHandler(rec.service(), &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{}))

			w := do(router, w8aRawCreateReq(w8aJSONString(t, tc.body), "tenant_admin"))

			prob := w8aProblem(t, w, http.StatusBadRequest, "BAD_REQUEST", "bad-request")
			assert.Equal(t, "request body has missing or invalid fields", prob["detail"])
			params := wfCreateInvalidParams(t, prob)
			require.Len(t, params, 1)
			assert.Equal(t, tc.field, params[0]["name"])
			assert.Equal(t, "is required", params[0]["reason"])
			assert.Empty(t, rec.recorded())
		})
	}
}

func TestW8aCreateWorkflow_DataAfterTheJSONValue_Is400NotValidJSON(t *testing.T) {
	valid := `{"key":"trailing-wf","name":"Trailing","bpmn_xml":"<definitions/>"}`
	cases := []struct {
		name string
		body string
	}{
		{name: "a second object", body: valid + `{"zzq_extra":1}`},
		{name: "a second object after a newline", body: valid + "\n" + valid},
		{name: "stray text", body: valid + " zzq"},
		{name: "a closing bracket", body: valid + "]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &w8aCreateRecorder{}
			router := newRouter(newHandler(rec.service(), &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{}))

			w := do(router, w8aRawCreateReq(tc.body, "tenant_admin"))

			prob := w8aProblem(t, w, http.StatusBadRequest, "BAD_REQUEST", "bad-request")
			assert.Equal(t, "request body is not valid JSON", prob["detail"])
			assert.NotContains(t, prob, "invalid_params")
			assert.Empty(t, rec.recorded(), "a body with data after its value never reaches the service")
		})
	}
}

func TestW8aCreateWorkflow_WithoutIdempotencyKey_TheSameBodyTwiceGivesADuplicateOnTheSecond(t *testing.T) {
	seen := map[string]bool{}
	rec := &w8aCreateRecorder{fn: func(c w8aCreateCall) error {
		if seen[c.key] {
			return domain.ErrDuplicateTemplateKey
		}
		seen[c.key] = true
		return nil
	}}
	cache := newMemCache()
	router := w8aCacheRouter(rec, cache)
	body := w8aJSONString(t, w8aCreateBody("twice-wf", "Twice", w8aCreateXML))

	first := do(router, w8aRawCreateReq(body, "tenant_admin"))
	second := do(router, w8aRawCreateReq(body, "tenant_admin"))

	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	w8aProblem(t, second, http.StatusConflict, "DUPLICATE_TEMPLATE_KEY", "conflict")
	calls := rec.recorded()
	require.Len(t, calls, 2, "without the header the handler runs every time")
	for _, c := range calls {
		assert.Equal(t, testTenantID, c.tenant)
		assert.Equal(t, testUserID, c.user)
		assert.Equal(t, "twice-wf", c.key)
	}
	assert.Empty(t, w8aStoredEntries(cache), "nothing is stored or reserved without the header")
}

func TestW8aCreateWorkflow_IdempotencyKeyLength_256IsAcceptedAndMoreIsRefused(t *testing.T) {
	cases := []struct {
		name       string
		keyLen     int
		wantStatus int
	}{
		{name: "exactly 256", keyLen: 256, wantStatus: http.StatusCreated},
		{name: "one more", keyLen: 257, wantStatus: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &w8aCreateRecorder{}
			cache := newMemCache()
			router := w8aCacheRouter(rec, cache)
			r := w8aRawCreateReq(w8aJSONString(t, w8aCreateBody("long-key-wf", "Long key", w8aCreateXML)), "tenant_admin")
			r.Header.Set("Idempotency-Key", strings.Repeat("k", tc.keyLen))

			w := do(router, r)

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			if tc.wantStatus == http.StatusCreated {
				assert.Contains(t, w.Body.String(), testWFID.String())
				require.Len(t, rec.recorded(), 1)
				assert.Equal(t, testTenantID, rec.recorded()[0].tenant)
				return
			}
			prob := w8aProblem(t, w, http.StatusBadRequest, "BAD_REQUEST", "bad-request")
			assert.Equal(t, "Idempotency-Key must be at most 256 bytes", prob["detail"])
			assert.Empty(t, rec.recorded())
			assert.Empty(t, w8aStoredEntries(cache))
		})
	}
}

func TestW8aCreateWorkflow_SameIdempotencyKey_OtherTenantOrUserRunsItsOwnCreate(t *testing.T) {
	rec := &w8aCreateRecorder{}
	cache := newMemCache()
	router := w8aCacheRouter(rec, cache)
	body := w8aJSONString(t, w8aCreateBody("shared-key-wf", "Shared key", w8aCreateXML))
	otherTenant := uuid.New()
	otherUser := uuid.New()
	send := func(tenant, user uuid.UUID) *httptest.ResponseRecorder {
		r := w8aRawCreateReq(body, "tenant_admin")
		r.Header.Set("x-tenant-id", tenant.String())
		r.Header.Set("x-user-id", user.String())
		r.Header.Set("Idempotency-Key", "shared-key")
		return do(router, r)
	}

	first := send(testTenantID, testUserID)
	replay := send(testTenantID, testUserID)
	fromOtherTenant := send(otherTenant, testUserID)
	fromOtherUser := send(testTenantID, otherUser)

	for name, w := range map[string]*httptest.ResponseRecorder{"first": first, "replay": replay, "other tenant": fromOtherTenant, "other user": fromOtherUser} {
		assert.Equal(t, http.StatusCreated, w.Code, "%s: %s", name, w.Body.String())
	}
	calls := rec.recorded()
	require.Len(t, calls, 3, "the same caller's repeat is replayed; another tenant and another user each run")
	assert.Equal(t, testTenantID, calls[0].tenant)
	assert.Equal(t, testUserID, calls[0].user)
	assert.Equal(t, otherTenant, calls[1].tenant)
	assert.Equal(t, testUserID, calls[1].user)
	assert.Equal(t, testTenantID, calls[2].tenant)
	assert.Equal(t, otherUser, calls[2].user)
	assert.Equal(t, first.Body.String(), replay.Body.String(), "the replay returns the stored response")
	assert.Len(t, w8aStoredEntries(cache), 3, "one stored response per tenant and user")
}
