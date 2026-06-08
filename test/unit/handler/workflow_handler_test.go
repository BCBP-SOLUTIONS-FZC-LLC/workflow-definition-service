package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func TestListWorkflows_OK(t *testing.T) {
	wf := newWorkflow()
	h := newHandler(&fakeWorkflowSvc{
		list: func(_ context.Context, _ uuid.UUID, _ port.WorkflowFilter) ([]*domain.Workflow, int64, error) {
			return []*domain.Workflow{wf}, 1, nil
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	pagination := body["pagination"].(map[string]any)
	assert.Equal(t, float64(1), pagination["total_count"])
	assert.Equal(t, float64(1), pagination["page"])
	assert.Equal(t, float64(20), pagination["limit"])
	workflows := body["workflows"].([]any)
	assert.Len(t, workflows, 1)
}

func TestListWorkflows_Filters(t *testing.T) {
	var capturedFilter port.WorkflowFilter
	h := newHandler(&fakeWorkflowSvc{
		list: func(_ context.Context, _ uuid.UUID, f port.WorkflowFilter) ([]*domain.Workflow, int64, error) {
			capturedFilter = f
			return nil, 0, nil
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	r := req(http.MethodGet, "/api/v1/workflows?search=foo&key=wf-1&is_valid=true&has_draft=false&status=archived&page=2&limit=5", nil)
	w := do(newRouter(h), r)
	assert.Equal(t, http.StatusOK, w.Code)

	require.NotNil(t, capturedFilter.Search)
	assert.Equal(t, "foo", *capturedFilter.Search)
	require.NotNil(t, capturedFilter.BusinessKey)
	assert.Equal(t, "wf-1", *capturedFilter.BusinessKey)
	require.NotNil(t, capturedFilter.IsValid)
	assert.True(t, *capturedFilter.IsValid)
	require.NotNil(t, capturedFilter.HasDraft)
	assert.False(t, *capturedFilter.HasDraft)
	require.NotNil(t, capturedFilter.Archived)
	assert.True(t, *capturedFilter.Archived)
	assert.Equal(t, 2, capturedFilter.Page)
	assert.Equal(t, 5, capturedFilter.Limit)
}

func TestListWorkflows_PaginateBounds(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	// page < 1 and limit > 100 → both clamped
	r := req(http.MethodGet, "/api/v1/workflows?page=0&limit=999", nil)
	w := do(newRouter(h), r)
	assert.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	pagination := body["pagination"].(map[string]any)
	assert.Equal(t, float64(1), pagination["page"])
	assert.Equal(t, float64(20), pagination["limit"])
}

func TestListWorkflows_PaginateLimitZero(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	r := req(http.MethodGet, "/api/v1/workflows?limit=0", nil)
	w := do(newRouter(h), r)
	assert.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	pagination := body["pagination"].(map[string]any)
	assert.Equal(t, float64(20), pagination["limit"])
}

func TestListWorkflows_ServiceError(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{
		list: func(_ context.Context, _ uuid.UUID, _ port.WorkflowFilter) ([]*domain.Workflow, int64, error) {
			return nil, 0, domain.ErrForbidden
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows", nil))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestListWorkflows_MissingCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newBareRouter(h), req(http.MethodGet, "/api/v1/workflows", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListWorkflows_InvalidTenantCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	r := req(http.MethodGet, "/api/v1/workflows", nil)
	r.Header.Set("x-tenant-id", "not-a-uuid")
	w := do(newRouter(h), r)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListWorkflows_InvalidUserCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	r := req(http.MethodGet, "/api/v1/workflows", nil)
	r.Header.Set("x-user-id", "not-a-uuid")
	w := do(newRouter(h), r)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreateWorkflow_OK(t *testing.T) {
	wf := newWorkflow()
	ver := newDraftVersion()

	h := newHandler(&fakeWorkflowSvc{
		create: func(_ context.Context, _ uuid.UUID, _ uuid.UUID, bk, name, desc, xml string) (*domain.Workflow, *domain.WorkflowVersion, error) {
			return wf, ver, nil
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	body := map[string]any{
		"key":      "wf-1",
		"name":     "My Workflow",
		"bpmn_xml": "<definitions/>",
	}
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows", body))
	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotNil(t, resp["workflow_id"])
	assert.NotNil(t, resp["version_id"])
	assert.NotNil(t, resp["status"])
}

func TestCreateWorkflow_BindError(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	// missing required fields
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows", map[string]any{"name": "x"}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var prob map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&prob))
	assert.Equal(t, "BAD_REQUEST", prob["code"])
}

func TestCreateWorkflow_DuplicateKey(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{
		create: func(_ context.Context, _ uuid.UUID, _ uuid.UUID, _, _, _, _ string) (*domain.Workflow, *domain.WorkflowVersion, error) {
			return nil, nil, domain.ErrDuplicateBusinessKey
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	body := map[string]any{"key": "x", "name": "y", "bpmn_xml": "<x/>"}
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows", body))
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestCreateWorkflow_PlanQuota(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{
		create: func(_ context.Context, _ uuid.UUID, _ uuid.UUID, _, _, _, _ string) (*domain.Workflow, *domain.WorkflowVersion, error) {
			return nil, nil, domain.ErrPlanQuotaExceeded
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	body := map[string]any{"key": "x", "name": "y", "bpmn_xml": "<x/>"}
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows", body))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestCreateWorkflow_IdempotencyReplay(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{
		create: func(_ context.Context, _ uuid.UUID, _ uuid.UUID, _, _, _, _ string) (*domain.Workflow, *domain.WorkflowVersion, error) {
			return nil, nil, domain.ErrIdempotencyKeyReplay
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	body := map[string]any{"key": "x", "name": "y", "bpmn_xml": "<x/>"}
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows", body))
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestCreateWorkflow_InternalError(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{
		create: func(_ context.Context, _ uuid.UUID, _ uuid.UUID, _, _, _, _ string) (*domain.Workflow, *domain.WorkflowVersion, error) {
			return nil, nil, errors.New("unexpected")
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	body := map[string]any{"key": "x", "name": "y", "bpmn_xml": "<x/>"}
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows", body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetWorkflow_OK(t *testing.T) {
	wf := newWorkflow()
	ver := newDraftVersion()

	h := newHandler(&fakeWorkflowSvc{
		get: func(_ context.Context, _ uuid.UUID, id uuid.UUID) (*domain.Workflow, []*domain.WorkflowVersion, error) {
			return wf, []*domain.WorkflowVersion{ver}, nil
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotNil(t, resp["id"])
	assert.NotNil(t, resp["key"])
	versions := resp["versions"].([]any)
	assert.Len(t, versions, 1)
}

func TestGetWorkflow_InvalidUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/not-a-uuid", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetWorkflow_NotFound(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{
		get: func(_ context.Context, _ uuid.UUID, _ uuid.UUID) (*domain.Workflow, []*domain.WorkflowVersion, error) {
			return nil, nil, domain.ErrNotFound
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetWorkflow_Unauthorized(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{
		get: func(_ context.Context, _ uuid.UUID, _ uuid.UUID) (*domain.Workflow, []*domain.WorkflowVersion, error) {
			return nil, nil, domain.ErrUnauthorized
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestArchiveWorkflow_OK(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{
		archive: func(_ context.Context, _, _, _ uuid.UUID) error { return nil },
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/"+testWFID.String()+"/archive", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestArchiveWorkflow_InvalidUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/bad/archive", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestArchiveWorkflow_ActiveInstances(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{
		archive: func(_ context.Context, _, _, _ uuid.UUID) error { return domain.ErrActiveInstancesExist },
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/"+testWFID.String()+"/archive", nil))
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestArchiveWorkflow_NoActiveVersion(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{
		archive: func(_ context.Context, _, _, _ uuid.UUID) error { return domain.ErrNoActiveVersion },
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/"+testWFID.String()+"/archive", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestArchiveWorkflow_UpstreamUnavailable(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{
		archive: func(_ context.Context, _, _, _ uuid.UUID) error { return domain.ErrUpstreamUnavailable },
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/"+testWFID.String()+"/archive", nil))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}
