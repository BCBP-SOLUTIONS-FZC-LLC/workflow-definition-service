package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestListVersions_OK(t *testing.T) {
	ver := newPublishedVersion()
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		list: func(_ context.Context, _, _ uuid.UUID, _, _ int) ([]*domain.WorkflowVersion, int64, error) {
			return []*domain.WorkflowVersion{ver}, 1, nil
		},
	}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"/versions", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	pagination := resp["pagination"].(map[string]any)
	assert.Equal(t, float64(1), pagination["total_count"])
	versions := resp["versions"].([]any)
	assert.Len(t, versions, 1)
}

func TestListVersions_ReturnsSummariesNotTheBPMNOrThePlan(t *testing.T) {
	ver := newPublishedVersion()
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		list: func(_ context.Context, _, _ uuid.UUID, _, _ int) ([]*domain.WorkflowVersion, int64, error) {
			return []*domain.WorkflowVersion{ver}, 1, nil
		},
	}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"/versions", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Versions []map[string]any `json:"versions"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Versions, 1)
	for _, heavy := range []string{"bpmn_xml", "compiled_plan_json", "module_refs", "validation_errors"} {
		assert.NotContains(t, resp.Versions[0], heavy, "a list page must not carry %s; the version read does", heavy)
	}
	for _, field := range []string{"id", "status", "is_valid", "created_by_user_id", "created_at"} {
		assert.Contains(t, resp.Versions[0], field)
	}
}

func TestListVersions_LargestPage_GivesAnOffsetTheColumnCanHold(t *testing.T) {
	var gotPage, gotLimit int
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		list: func(_ context.Context, _, _ uuid.UUID, page, limit int) ([]*domain.WorkflowVersion, int64, error) {
			gotPage, gotLimit = page, limit
			return nil, 0, nil
		},
	}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"/versions?page=100000&limit=100", nil))
	require.Equal(t, http.StatusOK, w.Code)

	assert.LessOrEqual(t, int64(gotPage-1)*int64(gotLimit), int64(math.MaxInt32), "page %d at limit %d gives an offset the database column cannot hold", gotPage, gotLimit)
}

func TestListVersions_InvalidWorkflowUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/bad/versions", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListVersions_ServiceError(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		list: func(_ context.Context, _, _ uuid.UUID, _, _ int) ([]*domain.WorkflowVersion, int64, error) {
			return nil, 0, domain.ErrNotFound
		},
	}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"/versions", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetVersion_OK(t *testing.T) {
	ver := newPublishedVersion()
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		get: func(_ context.Context, _, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
			return ver, nil
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String()
	w := do(newRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotNil(t, resp["id"])
	assert.NotNil(t, resp["workflow_id"])
}

func TestGetVersion_InvalidWorkflowUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/bad/versions/"+testVerID.String(), nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetVersion_InvalidVersionUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"/versions/bad", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetVersion_WithCompiledPlanAndValidationErrors(t *testing.T) {
	compiledPlan := `{"stages":[]}`
	validationErrors := `[{"node_id":"n1","error":"invalid dept"}]`
	ver := newPublishedVersion()
	ver.CompiledPlanJSON = &compiledPlan
	ver.ValidationErrorsJSON = &validationErrors

	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		get: func(_ context.Context, _, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
			return ver, nil
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String()
	w := do(newRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotNil(t, resp["compiled_plan_json"])
	errs, ok := resp["validation_errors"].([]any)
	assert.True(t, ok)
	assert.Len(t, errs, 1)
}

func TestGetVersion_ServiceError(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		get: func(_ context.Context, _, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
			return nil, domain.ErrNotFound
		},
	}, &fakeValidationSvc{})
	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String()
	w := do(newRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPublishVersion_Forbidden(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), req(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPublishVersion_OK_NoBody(t *testing.T) {
	ver := newPublishedVersion()
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		publish: func(_ context.Context, _, _, _, _ uuid.UUID, skip bool) (*domain.WorkflowVersion, error) {
			assert.False(t, skip)
			return ver, nil
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), adminReq(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPublishVersion_OK_WithSkip(t *testing.T) {
	ver := newPublishedVersion()
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		publish: func(_ context.Context, _, _, _, _ uuid.UUID, skip bool) (*domain.WorkflowVersion, error) {
			assert.True(t, skip)
			return ver, nil
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), adminReq(http.MethodPost, path, map[string]any{"force_publish_structural": true}))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPublishVersion_MalformedBody_IsA400AndPublishesNothing(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		publish: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, bool) (*domain.WorkflowVersion, error) {
			t.Error("a malformed body must not reach the service")
			return newPublishedVersion(), nil
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	r := adminReq(http.MethodPost, path, nil)
	r.Body = io.NopCloser(strings.NewReader(`{"force_publish_structural": tru`))

	w := do(newRouter(h), r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPublishVersion_InvalidVersionStatus(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		publish: func(_ context.Context, _, _, _, _ uuid.UUID, _ bool) (*domain.WorkflowVersion, error) {
			return nil, domain.ErrInvalidVersionStatus
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), adminReq(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusConflict, w.Code)
	var prob map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&prob))
	assert.Equal(t, "INVALID_VERSION_STATUS", prob["code"])
}

func TestPublishVersion_VersionNotDraft(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		publish: func(_ context.Context, _, _, _, _ uuid.UUID, _ bool) (*domain.WorkflowVersion, error) {
			return nil, domain.ErrVersionNotDraft
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), adminReq(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestPublishVersion_VersionAlreadyPublished(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		publish: func(_ context.Context, _, _, _, _ uuid.UUID, _ bool) (*domain.WorkflowVersion, error) {
			return nil, domain.ErrVersionAlreadyPublished
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), adminReq(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestPublishVersion_AssigneeIneligible(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		publish: func(_ context.Context, _, _, _, _ uuid.UUID, _ bool) (*domain.WorkflowVersion, error) {
			return nil, domain.ErrAssigneeIneligible
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), adminReq(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestPublishVersion_ValidationFailed(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		publish: func(_ context.Context, _, _, _, _ uuid.UUID, _ bool) (*domain.WorkflowVersion, error) {
			return nil, &domain.ValidationFailedError{
				Errors: []domain.BPMNValidationError{
					{Code: domain.BPMNErrNoStartEvent, NodeID: "n1", Message: "missing start event"},
				},
			}
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), adminReq(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	var prob map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&prob))
	assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
}

func TestCloneVersion_Forbidden(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/clone"
	w := do(newRouter(h), req(http.MethodPost, path, map[string]any{"new_key": "x", "new_name": "y"}))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCloneVersion_OK(t *testing.T) {
	wf := newWorkflow()
	ver := newDraftVersion()
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		clone: func(_ context.Context, _, _, _, _ uuid.UUID, _ string, r service.CloneReq) (*domain.Workflow, *domain.WorkflowVersion, error) {
			assert.Equal(t, "new-key", r.NewKey)
			return wf, ver, nil
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/clone"
	body := map[string]any{"new_key": "new-key", "new_name": "New Name"}
	w := do(newRouter(h), adminReq(http.MethodPost, path, body))
	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotNil(t, resp["workflow_id"])
	assert.NotNil(t, resp["version_id"])
	assert.NotNil(t, resp["status"])
	assert.Equal(t, "Version cloned", resp["message"])
}

func TestCloneVersion_PassesThePlanHeaderUntouched(t *testing.T) {
	for header, want := range map[string]string{"PRO": "PRO", "pro": "pro", "": "", "enterprise": "enterprise"} {
		t.Run(header, func(t *testing.T) {
			var got string
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
				clone: func(_ context.Context, _, _, _, _ uuid.UUID, planTier string, _ service.CloneReq) (*domain.Workflow, *domain.WorkflowVersion, error) {
					got = planTier
					return newWorkflow(), newDraftVersion(), nil
				},
			}, &fakeValidationSvc{})

			path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/clone"
			r := adminReq(http.MethodPost, path, map[string]any{"new_key": "k", "new_name": "n"})
			r.Header.Set("x-plan", header)
			w := do(newRouter(h), r)

			require.Equal(t, http.StatusCreated, w.Code)
			assert.Equal(t, want, got)
		})
	}
}

func TestCloneVersion_BindError(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/clone"
	// missing required new_key and new_name
	w := do(newRouter(h), adminReq(http.MethodPost, path, map[string]any{"new_description": "only desc"}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var prob map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&prob))
	assert.Equal(t, "BAD_REQUEST", prob["code"])
}

func TestCloneVersion_VersionNotPublished(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		clone: func(_ context.Context, _, _, _, _ uuid.UUID, _ string, _ service.CloneReq) (*domain.Workflow, *domain.WorkflowVersion, error) {
			return nil, nil, domain.ErrVersionNotPublished
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/clone"
	body := map[string]any{"new_key": "x", "new_name": "y"}
	w := do(newRouter(h), adminReq(http.MethodPost, path, body))
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestPromoteVersion_Forbidden(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/promote"
	w := do(newRouter(h), req(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPromoteVersion_OK(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		promote: func(_ context.Context, _, _, _, _ uuid.UUID, _ string) (*domain.WorkflowVersion, error) {
			return &domain.WorkflowVersion{}, nil
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/promote"
	w := do(newRouter(h), adminReq(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPromoteVersion_PassesThePlanCodeFromTheGateway(t *testing.T) {
	var got string
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		promote: func(_ context.Context, _, _, _, _ uuid.UUID, planCode string) (*domain.WorkflowVersion, error) {
			got = planCode
			return &domain.WorkflowVersion{}, nil
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/promote"
	r := adminReq(http.MethodPost, path, nil)
	r.Header.Set("x-plan", "pro")
	w := do(newRouter(h), r)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "pro", got)
}

func TestPromoteVersion_InvalidWorkflowUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/workflows/bad/versions/"+testVerID.String()+"/promote", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPromoteVersion_InvalidVersionUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/workflows/"+testWFID.String()+"/versions/bad/promote", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPromoteVersion_VersionNotPublished(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		promote: func(_ context.Context, _, _, _, _ uuid.UUID, _ string) (*domain.WorkflowVersion, error) {
			return nil, domain.ErrVersionNotPublished
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/promote"
	w := do(newRouter(h), adminReq(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestExportBPMN_OK(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		export: func(_ context.Context, _, _, _ uuid.UUID) (string, string, error) {
			return "<definitions/>", "my-workflow-v1.bpmn", nil
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/export"
	w := do(newRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/xml")
	assert.Contains(t, w.Header().Get("Content-Disposition"), "my-workflow-v1.bpmn")
	assert.Equal(t, "<definitions/>", w.Body.String())
}

func TestExportBPMN_InvalidVersionUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"/versions/bad/export", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestExportBPMN_ServiceError(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		export: func(_ context.Context, _, _, _ uuid.UUID) (string, string, error) {
			return "", "", domain.ErrNotFound
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/export"
	w := do(newRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetVersionDiff_OK(t *testing.T) {
	diffResult := &service.DiffResult{
		WorkflowID:      testWFID,
		BaseVersionID:   testVerID,
		TargetVersionID: testVerID2,
		ChangeType:      "STRUCTURAL",
		Changes: service.DiffChanges{
			AddedDepartments:   []string{"dept-1"},
			RemovedDepartments: []string{},
			StepChanges:        nil,
			MetadataOnly:       false,
		},
	}

	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		diff: func(_ context.Context, _, _, _, _ uuid.UUID) (*service.DiffResult, error) {
			return diffResult, nil
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/diff/" + testVerID2.String()
	w := do(newRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "STRUCTURAL", resp["change_type"])
	assert.NotNil(t, resp["changes"])
}

func TestGetVersionDiff_InvalidBaseUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	path := "/api/v1/workflows/" + testWFID.String() + "/versions/bad/diff/" + testVerID2.String()
	w := do(newRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetVersionDiff_InvalidTargetUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/diff/bad"
	w := do(newRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetVersionDiff_StructuralDivergence(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		diff: func(_ context.Context, _, _, _, _ uuid.UUID) (*service.DiffResult, error) {
			return nil, domain.ErrStructuralDivergence
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/diff/" + testVerID2.String()
	w := do(newRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusConflict, w.Code)
}
