package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
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
	assert.Equal(t, float64(1), resp["total"])
	versions := resp["versions"].([]any)
	assert.Len(t, versions, 1)
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
	assert.NotNil(t, resp["version"])
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

func TestPublishVersion_OK_NoBody(t *testing.T) {
	ver := newPublishedVersion()
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		publish: func(_ context.Context, _, _, _, _ uuid.UUID, skip bool) (*domain.WorkflowVersion, error) {
			assert.False(t, skip)
			return ver, nil
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), req(http.MethodPost, path, nil))
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
	w := do(newRouter(h), req(http.MethodPost, path, map[string]any{"skip_eligibility_check": true}))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPublishVersion_InvalidVersionStatus(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		publish: func(_ context.Context, _, _, _, _ uuid.UUID, _ bool) (*domain.WorkflowVersion, error) {
			return nil, domain.ErrInvalidVersionStatus
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), req(http.MethodPost, path, nil))
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
	w := do(newRouter(h), req(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestPublishVersion_VersionAlreadyPublished(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		publish: func(_ context.Context, _, _, _, _ uuid.UUID, _ bool) (*domain.WorkflowVersion, error) {
			return nil, domain.ErrVersionAlreadyPublished
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), req(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestPublishVersion_AssigneeIneligible(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		publish: func(_ context.Context, _, _, _, _ uuid.UUID, _ bool) (*domain.WorkflowVersion, error) {
			return nil, domain.ErrAssigneeIneligible
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), req(http.MethodPost, path, nil))
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
	w := do(newRouter(h), req(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	var prob map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&prob))
	assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
}

func TestCloneVersion_OK(t *testing.T) {
	wf := newWorkflow()
	ver := newDraftVersion()
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		clone: func(_ context.Context, _, _, _, _ uuid.UUID, r service.CloneReq) (*domain.Workflow, *domain.WorkflowVersion, error) {
			assert.Equal(t, "new-key", r.NewKey)
			return wf, ver, nil
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/clone"
	body := map[string]any{"new_key": "new-key", "new_name": "New Name"}
	w := do(newRouter(h), req(http.MethodPost, path, body))
	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotNil(t, resp["workflow"])
	assert.NotNil(t, resp["version"])
}

func TestCloneVersion_BindError(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/clone"
	// missing required new_key and new_name
	w := do(newRouter(h), req(http.MethodPost, path, map[string]any{"new_description": "only desc"}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var prob map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&prob))
	assert.Equal(t, "BAD_REQUEST", prob["code"])
}

func TestCloneVersion_VersionNotPublished(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		clone: func(_ context.Context, _, _, _, _ uuid.UUID, _ service.CloneReq) (*domain.Workflow, *domain.WorkflowVersion, error) {
			return nil, nil, domain.ErrVersionNotPublished
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/clone"
	body := map[string]any{"new_key": "x", "new_name": "y"}
	w := do(newRouter(h), req(http.MethodPost, path, body))
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestPromoteVersion_OK(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		promote: func(_ context.Context, _, _, _, _ uuid.UUID) error { return nil },
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/promote"
	w := do(newRouter(h), req(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestPromoteVersion_InvalidWorkflowUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/bad/versions/"+testVerID.String()+"/promote", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPromoteVersion_InvalidVersionUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/"+testWFID.String()+"/versions/bad/promote", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPromoteVersion_VersionNotPublished(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		promote: func(_ context.Context, _, _, _, _ uuid.UUID) error {
			return domain.ErrVersionNotPublished
		},
	}, &fakeValidationSvc{})

	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/promote"
	w := do(newRouter(h), req(http.MethodPost, path, nil))
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
