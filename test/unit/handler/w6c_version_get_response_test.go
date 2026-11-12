package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type w6cGetCall struct {
	calls                           int
	tenantID, workflowID, versionID uuid.UUID
}

func w6cGetVersionSvc(rec *w6cGetCall, v *domain.WorkflowVersion, err error) *fakeVersionSvc {
	return &fakeVersionSvc{
		get: func(_ context.Context, tenantID, workflowID, versionID uuid.UUID) (*domain.WorkflowVersion, error) {
			rec.calls++
			rec.tenantID, rec.workflowID, rec.versionID = tenantID, workflowID, versionID
			return v, err
		},
	}
}

func w6cGetVersionOK(t *testing.T, v *domain.WorkflowVersion) (map[string]any, *w6cGetCall) {
	t.Helper()
	rec := &w6cGetCall{}
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, w6cGetVersionSvc(rec, v, nil), &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, versionPath(testWFID.String(), testVerID.String()), nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body, rec
}

func w6cParseTime(t *testing.T, v any) time.Time {
	t.Helper()
	s, ok := v.(string)
	require.True(t, ok, "time field is %T, want string", v)
	parsed, err := time.Parse(time.RFC3339Nano, s)
	require.NoError(t, err)
	return parsed
}

func TestGetVersion_PublishedVersion_ReturnsTheStoredFieldsAndEmbedsThePlanAsJSON(t *testing.T) {
	t.Parallel()

	number := int32(3)
	publishedAt := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	createdAt := time.Date(2026, 5, 1, 7, 8, 9, 0, time.UTC)
	updatedAt := time.Date(2026, 5, 6, 7, 8, 10, 0, time.UTC)
	author := uuid.New()
	moduleRef := domain.ModuleRef{ModuleID: uuid.New(), VersionID: uuid.New(), ProcessID: "Process_W6c", VersionNumber: 2}
	plan := `{"main_plan":"w6c","plans":[{"name":"w6c"}]}`
	v := &domain.WorkflowVersion{
		ID: testVerID, WorkflowID: testWFID, TenantID: testTenantID,
		Status: domain.VersionStatusPublished, VersionNumber: &number,
		BPMNXML: "<w6c-definitions/>", ModuleRefs: []domain.ModuleRef{moduleRef},
		ArtifactHash: "w6c-hash-0123", IsValid: true, CompiledPlanJSON: &plan,
		CreatedByUserID: author, PublishedAt: &publishedAt, CreatedAt: createdAt, UpdatedAt: updatedAt,
		RecordVersion: 9,
	}

	body, rec := w6cGetVersionOK(t, v)

	assert.Equal(t, 1, rec.calls)
	assert.Equal(t, testTenantID, rec.tenantID)
	assert.Equal(t, testWFID, rec.workflowID)
	assert.Equal(t, testVerID, rec.versionID)
	assert.Equal(t, testVerID.String(), body["id"])
	assert.Equal(t, testWFID.String(), body["workflow_id"])
	assert.Equal(t, "PUBLISHED", body["status"])
	assert.Equal(t, float64(3), body["version_number"])
	assert.Equal(t, "<w6c-definitions/>", body["bpmn_xml"])
	assert.Equal(t, "w6c-hash-0123", body["artifact_hash"])
	assert.Equal(t, true, body["is_valid"])
	assert.Equal(t, author.String(), body["created_by_user_id"])
	assert.Equal(t, float64(9), body["record_version"])
	assert.True(t, publishedAt.Equal(w6cParseTime(t, body["published_at"])))
	assert.True(t, createdAt.Equal(w6cParseTime(t, body["created_at"])))
	assert.True(t, updatedAt.Equal(w6cParseTime(t, body["updated_at"])))
	assert.Equal(t, []any{map[string]any{
		"module_id": moduleRef.ModuleID.String(), "version_id": moduleRef.VersionID.String(),
		"process_id": "Process_W6c", "version_number": float64(2),
	}}, body["module_refs"])
	embedded, isObject := body["compiled_plan_json"].(map[string]any)
	require.True(t, isObject, "compiled_plan_json is %T, want an embedded JSON object", body["compiled_plan_json"])
	assert.Equal(t, "w6c", embedded["main_plan"])
	assert.NotContains(t, body, "validation_errors")
}

func TestGetVersion_DraftVersion_LeavesOutTheFourPublicationKeysInsteadOfSendingNull(t *testing.T) {
	t.Parallel()

	draft := newDraftVersion()
	draft.RecordVersion = 2

	body, rec := w6cGetVersionOK(t, draft)

	assert.Equal(t, testTenantID, rec.tenantID)
	for _, key := range []string{"version_number", "published_at", "artifact_hash", "compiled_plan_json"} {
		assert.NotContains(t, body, key)
	}
	assert.Equal(t, testVerID.String(), body["id"])
	assert.Equal(t, testWFID.String(), body["workflow_id"])
	assert.Equal(t, "DRAFT", body["status"])
	assert.Equal(t, "<definitions/>", body["bpmn_xml"])
	assert.Equal(t, true, body["is_valid"])
	assert.Equal(t, float64(2), body["record_version"])
	assert.Equal(t, []any{}, body["module_refs"], "module_refs is an empty array, not null")
	assert.NotContains(t, body, "validation_errors")
	for key, value := range body {
		assert.NotNil(t, value, "key %s is present with a null value", key)
	}
}

func TestGetVersion_StoredPlanOrErrorsNullOrEmpty_LeavesThoseKeysOut(t *testing.T) {
	t.Parallel()

	empty := ""
	plan := `{"main_plan":"w6c"}`
	errs := `[{"node_id":"N1","error":"w6c problem"}]`
	cases := []struct {
		name            string
		plan, errs      *string
		wantPlan        bool
		wantValidations bool
	}{
		{name: "both null", plan: nil, errs: nil},
		{name: "both empty strings", plan: &empty, errs: &empty},
		{name: "empty plan beside stored errors", plan: &empty, errs: &errs, wantValidations: true},
		{name: "stored plan beside empty errors", plan: &plan, errs: &empty, wantPlan: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := newPublishedVersion()
			v.CompiledPlanJSON, v.ValidationErrorsJSON = tc.plan, tc.errs

			body, _ := w6cGetVersionOK(t, v)

			assert.Equal(t, tc.wantPlan, body["compiled_plan_json"] != nil)
			assert.Equal(t, tc.wantValidations, body["validation_errors"] != nil)
			if !tc.wantPlan {
				assert.NotContains(t, body, "compiled_plan_json")
			}
			if !tc.wantValidations {
				assert.NotContains(t, body, "validation_errors")
			}
			assert.Equal(t, "PUBLISHED", body["status"])
		})
	}
}

func TestGetVersion_StoredAsInvalid_ReturnsEachValidationErrorWithItsNodeAndMessage(t *testing.T) {
	t.Parallel()

	stored := `[{"node_id":"Task_W6c_1","error":"department is missing"},{"node_id":"Gateway_W6c_2","error":"flow has no condition"}]`
	v := newPublishedVersion()
	v.IsValid = false
	v.ValidationErrorsJSON = &stored

	body, _ := w6cGetVersionOK(t, v)

	assert.Equal(t, false, body["is_valid"])
	assert.Equal(t, []any{
		map[string]any{"node_id": "Task_W6c_1", "error": "department is missing"},
		map[string]any{"node_id": "Gateway_W6c_2", "error": "flow has no condition"},
	}, body["validation_errors"])
}

func TestGetVersion_RepositoryReturnsAPlainError_Returns500WithTheGenericDetail(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{name: "plain error", err: errors.New("w6c pq: connection to 10.1.2.3:5432 refused")},
		{name: "wrapped error", err: fmt.Errorf("get version: %w", errors.New("w6c scan: unexpected column type"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &w6cGetCall{}
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, w6cGetVersionSvc(rec, nil, tc.err), &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, versionPath(testWFID.String(), testVerID.String()), nil))

			require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "INTERNAL_ERROR", prob["code"])
			assert.Equal(t, "https://api.workflow.platform/errors/internal-error", prob["type"])
			assert.Equal(t, "an unexpected error occurred", prob["detail"])
			assert.EqualValues(t, http.StatusInternalServerError, prob["status"])
			assert.NotContains(t, w.Body.String(), tc.err.Error())
			assert.NotContains(t, w.Body.String(), "10.1.2.3")
			assert.NotContains(t, prob, "bpmn_xml")
			assert.Equal(t, testTenantID, rec.tenantID)
			assert.Equal(t, testWFID, rec.workflowID)
			assert.Equal(t, testVerID, rec.versionID)
		})
	}
}

func TestGetVersion_InjectionStyleVersionID_IsRefusedAsNotAUUIDBeforeTheService(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"1;DROP TABLE", "1' OR '1'='1"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			rec := &w6cGetCall{}
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, w6cGetVersionSvc(rec, newPublishedVersion(), nil), &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, versionPath(testWFID.String(), id), nil))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, "https://api.workflow.platform/errors/bad-request", prob["type"])
			assert.Equal(t, "invalid version_id: not a valid UUID", prob["detail"])
			assert.Zero(t, rec.calls)
			assert.Equal(t, "/api/v1/workflows/"+testWFID.String()+"/versions/"+id, prob["instance"])
			assert.Contains(t, w.Header().Get("Content-Type"), "application/problem+json")
		})
	}
}
