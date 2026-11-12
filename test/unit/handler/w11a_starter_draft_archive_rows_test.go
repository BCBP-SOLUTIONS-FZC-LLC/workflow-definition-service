package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const (
	w11aTypeBadRequest = "https://api.workflow.platform/errors/bad-request"
	w11aTypeInternal   = "https://api.workflow.platform/errors/internal-error"
)

func w11aDraftPath() string {
	return "/api/v1/workflows/" + testWFID.String() + "/draft"
}

func w11aGetDraftWith(t *testing.T, draft *domain.WorkflowVersion) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
			return draft, nil
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, w11aDraftPath(), nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return w, body
}

func w11aStarterFromVersionRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/starters/from-workflow-version/"+testVerID.String(), strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", testTenantID.String())
	r.Header.Set("x-user-id", testUserID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	return r
}

func TestCreateStarterFromWorkflowVersion_BadBody_Returns400ProblemNamingOnlyTheFieldAndTheReason(t *testing.T) {
	t.Parallel()

	type param struct{ name, reason string }
	cases := []struct {
		name       string
		body       string
		wantDetail string
		wantParams []param
	}{
		{
			name: "body is not json", body: `{"name": `,
			wantDetail: "request body is not valid JSON",
		},
		{
			name: "name is a number", body: `{"name": 4017}`,
			wantDetail: "request body has a field of the wrong type",
			wantParams: []param{{"name", "has the wrong type"}},
		},
		{
			name: "name is an empty string", body: `{"name": ""}`,
			wantDetail: "request body has missing or invalid fields",
			wantParams: []param{{"name", "is required"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			log := &w8dLog{}
			h := handler.New(handler.Services{
				Starters: &fakeStarterSvc{
					createFromWorkflowVersion: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.CreateFromWorkflowVersionReq) (*domain.StarterTemplate, error) {
						calls++
						return nil, nil
					},
				},
				Log: log,
			})

			w := do(newRouter(h), w11aStarterFromVersionRequest(tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, w11aTypeBadRequest, prob["type"])
			assert.EqualValues(t, http.StatusBadRequest, prob["status"])
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, tc.wantDetail, prob["detail"])
			assert.Equal(t, "/api/v1/starters/from-workflow-version/"+testVerID.String(), prob["instance"])
			if tc.wantParams == nil {
				assert.NotContains(t, prob, "invalid_params")
			} else {
				raw, ok := prob["invalid_params"].([]any)
				require.True(t, ok, "invalid_params = %v", prob["invalid_params"])
				var got []param
				for _, item := range raw {
					m := item.(map[string]any)
					got = append(got, param{fmt.Sprint(m["name"]), fmt.Sprint(m["reason"])})
				}
				assert.Equal(t, tc.wantParams, got)
			}
			assert.NotContains(t, w.Body.String(), "4017")
			assert.NotContains(t, w.Body.String(), "createStarterFromWorkflowVersionReq")
			assert.Zero(t, calls, "the service is not reached")
			warned := log.warnLines("invalid request body")
			require.Len(t, warned, 1, "the raw bind error is logged once, not returned")
			assert.Equal(t, "/api/v1/starters/from-workflow-version/:version_id", warned[0].fields["path"])
		})
	}
}

func TestArchiveWorkflow_BothAdminRoles_ReachTheArchiveAndAnswerTheArchivedBody(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		roles string
	}{
		{name: "tenant admin", roles: "tenant_admin"},
		{name: "tenant owner", roles: "tenant_owner"},
		{name: "owner among other roles", roles: "tenant_member,tenant_owner"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			type call struct{ tenant, user, workflow uuid.UUID }
			var calls []call
			h := newHandler(&fakeWorkflowSvc{
				archive: func(_ context.Context, tenantID, userID, id uuid.UUID) error {
					calls = append(calls, call{tenantID, userID, id})
					return nil
				},
			}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
			r := req(http.MethodPost, "/api/v1/workflows/"+testWFID.String()+"/archive", nil)
			r.Header.Set("x-tenant-roles", tc.roles)

			w := do(newRouter(h), r)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, map[string]any{
				"workflow_id": testWFID.String(),
				"status":      "ARCHIVED",
				"message":     "Workflow archived successfully",
			}, body)
			assert.Equal(t, []call{{testTenantID, testUserID, testWFID}}, calls)
		})
	}
}

func TestGetDraft_StoredDraft_AnswersEveryFieldOfTheVersion(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	updated := time.Date(2026, 3, 5, 6, 7, 8, 0, time.UTC)
	draft := &domain.WorkflowVersion{
		ID:              testVerID,
		WorkflowID:      testWFID,
		TenantID:        testTenantID,
		Status:          domain.VersionStatusDraft,
		BPMNXML:         `<definitions id="w11a-draft"/>`,
		IsValid:         true,
		CreatedByUserID: testUserID,
		RecordVersion:   7,
		CreatedAt:       created,
		UpdatedAt:       updated,
	}
	type call struct{ tenant, workflow uuid.UUID }
	var calls []call
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		get: func(_ context.Context, tenantID, workflowID uuid.UUID) (*domain.WorkflowVersion, error) {
			calls = append(calls, call{tenantID, workflowID})
			return draft, nil
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, w11aDraftPath(), nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, map[string]any{
		"id":                 testVerID.String(),
		"workflow_id":        testWFID.String(),
		"status":             "DRAFT",
		"bpmn_xml":           `<definitions id="w11a-draft"/>`,
		"module_refs":        []any{},
		"is_valid":           true,
		"created_by_user_id": testUserID.String(),
		"created_at":         "2026-03-04T05:06:07Z",
		"updated_at":         "2026-03-05T06:07:08Z",
		"record_version":     float64(7),
	}, body, "a draft has no version number, hash, publication time, plan or validation errors to show")
	assert.Equal(t, []call{{testTenantID, testWFID}}, calls)
}

func TestGetDraft_InvalidDraftWithStoredValidationErrors_ShowsIsValidFalseAndEachError(t *testing.T) {
	t.Parallel()
	draft := newDraftVersion()
	draft.IsValid = false
	stored := `[{"node_id":"task_zq1","error":"task has no role"},{"node_id":"gateway_zq2","error":"gateway has no default flow"}]`
	draft.ValidationErrorsJSON = &stored
	log := &w8dLog{}
	h := handler.New(handler.Services{
		Drafts: &fakeDraftSvc{
			get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) { return draft, nil },
		},
		Log: log,
	})

	w := do(newRouter(h), req(http.MethodGet, w11aDraftPath(), nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, false, body["is_valid"])
	assert.Equal(t, []any{
		map[string]any{"node_id": "task_zq1", "error": "task has no role"},
		map[string]any{"node_id": "gateway_zq2", "error": "gateway has no default flow"},
	}, body["validation_errors"])
	assert.Empty(t, log.warnLines("stored validation errors could not be decoded; leaving them out of the response"))
}

func TestGetDraft_NoStoredValidationErrors_OmitsTheKeyAndStaysValid(t *testing.T) {
	t.Parallel()
	empty := ""
	cases := []struct {
		name   string
		stored *string
	}{
		{name: "null column", stored: nil},
		{name: "empty string", stored: &empty},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			draft := newDraftVersion()
			draft.ValidationErrorsJSON = tc.stored

			w, body := w11aGetDraftWith(t, draft)

			assert.NotContains(t, body, "validation_errors")
			assert.NotContains(t, w.Body.String(), "validation_errors")
			assert.Equal(t, true, body["is_valid"])
		})
	}
}

func TestGetDraft_NoStoredPlan_OmitsTheCompiledPlanKey(t *testing.T) {
	t.Parallel()
	empty := ""
	cases := []struct {
		name   string
		stored *string
	}{
		{name: "null column", stored: nil},
		{name: "empty string", stored: &empty},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			draft := newDraftVersion()
			draft.CompiledPlanJSON = tc.stored

			w, body := w11aGetDraftWith(t, draft)

			assert.NotContains(t, body, "compiled_plan_json")
			assert.NotContains(t, w.Body.String(), "compiled_plan_json")
		})
	}
}

func TestGetDraft_StoredPlan_IsEmbeddedAsAJSONObjectNotAnEscapedString(t *testing.T) {
	t.Parallel()
	draft := newDraftVersion()
	plan := `{"stages":[{"id":"stage_zq1","nodes":["a","b"]}],"limit":3}`
	draft.CompiledPlanJSON = &plan

	w, body := w11aGetDraftWith(t, draft)

	embedded, ok := body["compiled_plan_json"].(map[string]any)
	require.True(t, ok, "compiled_plan_json = %#v", body["compiled_plan_json"])
	assert.Equal(t, map[string]any{
		"stages": []any{map[string]any{"id": "stage_zq1", "nodes": []any{"a", "b"}}},
		"limit":  float64(3),
	}, embedded)
	assert.Contains(t, w.Body.String(), `"compiled_plan_json":{"stages":[`)
}

func TestGetDraft_UnrecognisedServiceError_Returns500WithoutTheInternalTextAndLogsIt(t *testing.T) {
	t.Parallel()
	const leaked = "w11a zzq storage controller exploded"
	cases := []struct {
		name string
		err  error
	}{
		{name: "bare error", err: errors.New(leaked)},
		{name: "wrapped by the service", err: fmt.Errorf("get draft: %w", errors.New(leaked))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			log := &w8dLog{}
			h := handler.New(handler.Services{
				Drafts: &fakeDraftSvc{
					get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) { return nil, tc.err },
				},
				Log: log,
			})

			w := do(newRouter(h), req(http.MethodGet, w11aDraftPath(), nil))

			require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, w11aTypeInternal, prob["type"])
			assert.EqualValues(t, http.StatusInternalServerError, prob["status"])
			assert.Equal(t, "INTERNAL_ERROR", prob["code"])
			assert.Equal(t, "an unexpected error occurred", prob["detail"])
			assert.Equal(t, w11aDraftPath(), prob["instance"])
			assert.NotContains(t, w.Body.String(), leaked)
			logged := log.errorLines("request failed")
			require.Len(t, logged, 1)
			assert.EqualValues(t, http.StatusInternalServerError, logged[0].fields["status"])
			assert.Equal(t, "INTERNAL_ERROR", logged[0].fields["code"])
			assert.Equal(t, testTenantID.String(), logged[0].fields["tenant_id"])
			assert.Equal(t, testUserID.String(), logged[0].fields["user_id"])
			assert.Equal(t, w11aDraftPath(), logged[0].fields["path"])
			assert.Contains(t, fmt.Sprint(logged[0].fields["error"]), leaked)
		})
	}
}

func TestGetDraft_NoIdentityOnTheRequest_Returns500MissingRequestContextWithoutReadingTheDraft(t *testing.T) {
	t.Parallel()
	calls := 0
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
			calls++
			return newDraftVersion(), nil
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newBareRouter(h), req(http.MethodGet, w11aDraftPath(), nil))

	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	prob := decodeProblem(t, w)
	assert.Equal(t, w11aTypeInternal, prob["type"])
	assert.EqualValues(t, http.StatusInternalServerError, prob["status"])
	assert.Equal(t, "INTERNAL_ERROR", prob["code"])
	assert.Equal(t, "missing request context", prob["detail"])
	assert.Equal(t, w11aDraftPath(), prob["instance"])
	assert.Zero(t, calls)
}

func TestGetDraft_IdNotAUUID_Returns400WithTheFixedDetailAndReadsNothing(t *testing.T) {
	t.Parallel()
	calls := 0
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
			calls++
			return newDraftVersion(), nil
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/not-a-uuid/draft", nil))

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	prob := decodeProblem(t, w)
	assert.Equal(t, w11aTypeBadRequest, prob["type"])
	assert.EqualValues(t, http.StatusBadRequest, prob["status"])
	assert.Equal(t, "BAD_REQUEST", prob["code"])
	assert.Equal(t, "invalid id: not a valid UUID", prob["detail"])
	assert.Equal(t, "/api/v1/workflows/not-a-uuid/draft", prob["instance"])
	assert.NotContains(t, prob, "invalid_params")
	assert.Zero(t, calls)
}

func TestInitDraft_Created_AnswersTheDraftIdentityWithANullVersionNumber(t *testing.T) {
	t.Parallel()
	for _, roles := range []string{"tenant_admin", "tenant_owner"} {
		t.Run(roles, func(t *testing.T) {
			t.Parallel()
			draft := newDraftVersion()
			type call struct{ tenant, user, workflow uuid.UUID }
			var calls []call
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
				init: func(_ context.Context, tenantID, userID, workflowID uuid.UUID) (*domain.WorkflowVersion, error) {
					calls = append(calls, call{tenantID, userID, workflowID})
					return draft, nil
				},
			}, &fakeVersionSvc{}, &fakeValidationSvc{})
			r := req(http.MethodPost, w11aDraftPath(), nil)
			r.Header.Set("x-tenant-roles", roles)

			w := do(newRouter(h), r)

			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, map[string]any{
				"workflow_id":    testWFID.String(),
				"version_id":     testVerID.String(),
				"status":         "DRAFT",
				"version_number": nil,
				"message":        "Draft initialized",
			}, body)
			assert.Contains(t, w.Body.String(), `"version_number":null`)
			assert.Equal(t, []call{{testTenantID, testUserID, testWFID}}, calls)
		})
	}
}
