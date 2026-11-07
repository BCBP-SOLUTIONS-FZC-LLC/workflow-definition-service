package handler_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const wfCreatePath = "/api/v1/workflows"

type wfCreateCall struct {
	key, name, description, bpmnXML string
	tenant, user                    uuid.UUID
	plan                            string
}

type wfCreateRecorder struct {
	calls []wfCreateCall
}

func (r *wfCreateRecorder) service() *fakeWorkflowSvc {
	svc := &fakeWorkflowSvc{}
	svc.create = func(_ context.Context, tenantID, userID uuid.UUID, key, name, desc, xml string) (*domain.Workflow, *domain.WorkflowVersion, error) {
		r.calls = append(r.calls, wfCreateCall{
			key: key, name: name, description: desc, bpmnXML: xml,
			tenant: tenantID, user: userID, plan: svc.lastPlanTier,
		})
		return newWorkflow(), newDraftVersion(), nil
	}
	return svc
}

func wfCreateBody(key, name, description string) map[string]any {
	body := map[string]any{"key": key, "name": name, "bpmn_xml": "<definitions/>"}
	if description != "" {
		body["description"] = description
	}
	return body
}

func wfCreateInvalidParams(t *testing.T, prob map[string]any) []map[string]any {
	t.Helper()
	raw, ok := prob["invalid_params"].([]any)
	require.True(t, ok, "problem body carries invalid_params: %v", prob)
	out := make([]map[string]any, len(raw))
	for i, p := range raw {
		out[i], ok = p.(map[string]any)
		require.True(t, ok)
	}
	return out
}

func TestCreateWorkflow_FieldLengths_OverTheLimitIsRefusedNamed(t *testing.T) {
	cases := []struct {
		name        string
		key         string
		wfName      string
		description string
		wantParams  []map[string]string
	}{
		{
			name:       "key one over the limit",
			key:        strings.Repeat("a", 256),
			wfName:     "Review",
			wantParams: []map[string]string{{"name": "key", "reason": "must not exceed 255 characters"}},
		},
		{
			name:       "name one over the limit",
			key:        "review",
			wfName:     strings.Repeat("n", 256),
			wantParams: []map[string]string{{"name": "name", "reason": "must not exceed 255 characters"}},
		},
		{
			name:        "description one over the limit",
			key:         "review",
			wfName:      "Review",
			description: strings.Repeat("d", 2001),
			wantParams:  []map[string]string{{"name": "description", "reason": "must not exceed 2000 characters"}},
		},
		{
			name:        "every field over the limit is listed together in field order",
			key:         strings.Repeat("a", 256),
			wfName:      strings.Repeat("n", 256),
			description: strings.Repeat("d", 2001),
			wantParams: []map[string]string{
				{"name": "key", "reason": "must not exceed 255 characters"},
				{"name": "name", "reason": "must not exceed 255 characters"},
				{"name": "description", "reason": "must not exceed 2000 characters"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &wfCreateRecorder{}
			router := newRouter(newHandler(rec.service(), &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{}))

			w := do(router, adminReq(http.MethodPost, wfCreatePath, wfCreateBody(tc.key, tc.wfName, tc.description)))

			require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "INVALID_INPUT", prob["code"])
			assert.Equal(t, "request fields exceed maximum length", prob["detail"])
			assert.True(t, strings.HasSuffix(prob["type"].(string), "/errors/validation-failed"), "type = %v", prob["type"])
			params := wfCreateInvalidParams(t, prob)
			require.Len(t, params, len(tc.wantParams))
			for i, want := range tc.wantParams {
				assert.Equal(t, want["name"], params[i]["name"])
				assert.Equal(t, want["reason"], params[i]["reason"])
			}
			assert.Empty(t, rec.calls, "an over-long field is refused before the service runs")
		})
	}
}

func TestCreateWorkflow_FieldLengths_AtTheLimitIsAcceptedUnchanged(t *testing.T) {
	cases := []struct {
		name        string
		key         string
		wfName      string
		description string
	}{
		{
			name:        "key and name of 255 and description of 2000 characters",
			key:         strings.Repeat("a", 255),
			wfName:      strings.Repeat("n", 255),
			description: strings.Repeat("d", 2000),
		},
		{
			name:        "multibyte characters count once each, not per byte",
			key:         strings.Repeat("é", 255),
			wfName:      strings.Repeat("é", 255),
			description: strings.Repeat("é", 2000),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &wfCreateRecorder{}
			router := newRouter(newHandler(rec.service(), &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{}))

			r := adminReq(http.MethodPost, wfCreatePath, wfCreateBody(tc.key, tc.wfName, tc.description))
			r.Header.Set("x-plan", "enterprise")
			w := do(router, r)

			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			require.Len(t, rec.calls, 1)
			assert.Equal(t, testTenantID, rec.calls[0].tenant)
			assert.Equal(t, testUserID, rec.calls[0].user)
			assert.Equal(t, "enterprise", rec.calls[0].plan)
			assert.Equal(t, tc.key, rec.calls[0].key)
			assert.Equal(t, tc.wfName, rec.calls[0].name)
			assert.Equal(t, tc.description, rec.calls[0].description)
		})
	}
}

func TestCreateWorkflow_WhitespaceOnlyBody_Returns400(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "spaces and a newline", body: "   \n"},
		{name: "tabs and carriage returns", body: "\t\r\n\t"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &wfCreateRecorder{}
			router := newRouter(newHandler(rec.service(), &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{}))
			r := adminReq(http.MethodPost, wfCreatePath, nil)
			r.Body = io.NopCloser(strings.NewReader(tc.body))

			w := do(router, r)

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, "request body is not valid JSON", prob["detail"])
			assert.Empty(t, rec.calls)
		})
	}
}

func TestCreateWorkflow_RoleHeader_DecidesWhoMayCreate(t *testing.T) {
	cases := []struct {
		name       string
		roles      string
		wantStatus int
	}{
		{name: "tenant owner", roles: "tenant_owner", wantStatus: http.StatusCreated},
		{name: "tenant owner among other roles", roles: "tenant_member,tenant_owner", wantStatus: http.StatusCreated},
		{name: "a role that is not an admin role", roles: "tenant_member", wantStatus: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &wfCreateRecorder{}
			router := newRouter(newHandler(rec.service(), &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{}))
			r := req(http.MethodPost, wfCreatePath, wfCreateBody("owner-wf", "Owner workflow", ""))
			r.Header.Set("x-tenant-roles", tc.roles)

			w := do(router, r)

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			if tc.wantStatus == http.StatusCreated {
				assert.Contains(t, w.Body.String(), testWFID.String())
				require.Len(t, rec.calls, 1)
				assert.Equal(t, "owner-wf", rec.calls[0].key)
				return
			}
			assert.Equal(t, "FORBIDDEN", decodeProblem(t, w)["code"])
			assert.Empty(t, rec.calls)
		})
	}
}

func TestCreateWorkflow_CacheReservationUnavailable_RunsWithoutIdempotencyAndWarns(t *testing.T) {
	rec := &wfCreateRecorder{}
	log := &fakeLogger{}
	cache := &fakeCache{
		get:   func(context.Context, string) (string, error) { return "", nil },
		setnx: func(context.Context, string, string, time.Duration) (bool, error) { return false, assert.AnError },
	}
	router := newRouter(handler.New(handler.Services{
		Workflows: rec.service(), Cache: cache, Log: log, IdempotencyTTL: time.Hour,
	}))
	r := adminReq(http.MethodPost, wfCreatePath, wfCreateBody("cache-down", "Cache down", ""))
	r.Header.Set("Idempotency-Key", "reservation-down")

	w := do(router, r)

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), testWFID.String())
	require.Len(t, rec.calls, 1, "the workflow is created although the reservation failed")
	assert.Contains(t, log.warnCalls, "idempotency: reservation unavailable, running without it")
}

func TestCreateWorkflow_FailedAttemptWhoseLockReleaseFails_BlocksTheRetryUntilTheLockExpires(t *testing.T) {
	mem := newMemCache()
	cache := (*fakeCache)(nil).wrap(mem)
	cache.delIfEqual = func(context.Context, string, string) error { return assert.AnError }
	log := &fakeLogger{}
	calls := 0
	svc := &fakeWorkflowSvc{
		create: func(context.Context, uuid.UUID, uuid.UUID, string, string, string, string) (*domain.Workflow, *domain.WorkflowVersion, error) {
			calls++
			if calls == 1 {
				return nil, nil, &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{Code: "MISSING_START", Message: "no start event"}}}
			}
			return newWorkflow(), newDraftVersion(), nil
		},
	}
	router := newRouter(handler.New(handler.Services{
		Workflows: svc, Cache: cache, Log: log, IdempotencyTTL: time.Hour,
	}))
	send := func() *httptest.ResponseRecorder {
		r := adminReq(http.MethodPost, wfCreatePath, wfCreateBody("lock-stuck", "Lock stuck", ""))
		r.Header.Set("Idempotency-Key", "stuck-lock")
		return do(router, r)
	}

	first := send()
	require.Equal(t, http.StatusUnprocessableEntity, first.Code, first.Body.String())
	assert.Contains(t, log.warnCalls, "idempotency: releasing the reservation failed, it expires on its own")
	pending := pendingKeys(mem)
	require.Len(t, pending, 1, "the failed release leaves the pending lock behind")

	retry := send()
	require.Equal(t, http.StatusConflict, retry.Code, retry.Body.String())
	assert.Equal(t, "IDEMPOTENCY_REQUEST_IN_PROGRESS", decodeProblem(t, retry)["code"])
	assert.Equal(t, 1, calls, "the retry inside the lock's lifetime does not reach the service")

	require.NoError(t, mem.Del(context.Background(), pending...))
	afterExpiry := send()
	require.Equal(t, http.StatusCreated, afterExpiry.Code, afterExpiry.Body.String())
	assert.Equal(t, 2, calls, "once the lock has expired the retry runs")
}

type wfCreateWiring struct {
	versions *mocks.MockModuleVersionRepository
	compiler *mocks.MockPlanCompiler
	router   *gin.Engine
}

func newWfCreateWiring(t *testing.T) *wfCreateWiring {
	t.Helper()
	ctrl := gomock.NewController(t)
	w := &wfCreateWiring{
		versions: mocks.NewMockModuleVersionRepository(ctrl),
		compiler: mocks.NewMockPlanCompiler(ctrl),
	}
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor:     mocks.NewMockTransactor(ctrl),
		Workflows:      mocks.NewMockWorkflowRepository(ctrl),
		Versions:       mocks.NewMockWorkflowVersionRepository(ctrl),
		Compiler:       w.compiler,
		ModuleVersions: w.versions,
	})
	w.router = newRouter(handler.New(handler.Services{Workflows: svc}))
	return w
}

func TestCreateWorkflow_ModuleLookupFailure_MapsToTheDependencyStatusAndWritesNothing(t *testing.T) {
	const leaked = "pq: relation module_version is corrupt"
	cases := []struct {
		name       string
		lookupErr  error
		wantStatus int
		wantCode   string
		leakedText string
	}{
		{
			name:       "a database error is an internal error without its text",
			lookupErr:  errors.New(leaked),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			leakedText: leaked,
		},
		{
			name:       "an unreachable database is an unavailable upstream",
			lookupErr:  fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			leakedText: "connection refused",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWfCreateWiring(t)
			calls := []domain.ModuleCall{{CallID: "Call_1", ProcessID: "Process_Missing"}}
			w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<definitions/>").Return(calls, nil, nil)
			w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Missing", int32(0)).Return(nil, tc.lookupErr)

			rec := do(w.router, adminReq(http.MethodPost, wfCreatePath, wfCreateBody("lookup-down", "Lookup down", "")))

			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			assert.Equal(t, tc.wantCode, decodeProblem(t, rec)["code"])
			assert.NotContains(t, rec.Body.String(), tc.leakedText)
		})
	}
}
