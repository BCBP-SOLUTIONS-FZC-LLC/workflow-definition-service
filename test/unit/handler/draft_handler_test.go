package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestGetDraft_OK(t *testing.T) {
	draft := newDraftVersion()
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		get: func(_ context.Context, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
			return draft, nil
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"/draft", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotNil(t, resp["draft"])
}

func TestGetDraft_InvalidUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/bad/draft", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetDraft_NoDraftExists(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		get: func(_ context.Context, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
			return nil, domain.ErrNoDraftExists
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"/draft", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
	var prob map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&prob))
	assert.Equal(t, "DRAFT_NOT_FOUND", prob["code"])
}

func TestInitDraft_OK(t *testing.T) {
	draft := newDraftVersion()
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		init: func(_ context.Context, _, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
			return draft, nil
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/"+testWFID.String()+"/draft", nil))
	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotNil(t, resp["draft"])
}

func TestInitDraft_InvalidUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/bad/draft", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestInitDraft_DraftAlreadyExists(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		init: func(_ context.Context, _, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
			return nil, domain.ErrDraftAlreadyExists
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/"+testWFID.String()+"/draft", nil))
	assert.Equal(t, http.StatusConflict, w.Code)
	var prob map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&prob))
	assert.Equal(t, "DRAFT_ALREADY_EXISTS", prob["code"])
}

func TestInitDraft_NotFound(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		init: func(_ context.Context, _, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
			return nil, domain.ErrNotFound
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/"+testWFID.String()+"/draft", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateDraft_OK(t *testing.T) {
	newName := "Updated Name"
	draft := newDraftVersion()
	draft.BPMNXML = "<updated/>"

	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		update: func(_ context.Context, _, _, _ uuid.UUID, r service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
			assert.NotNil(t, r.Name)
			assert.Equal(t, newName, *r.Name)
			return draft, nil
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	now := time.Now()
	body := map[string]any{
		"name":            newName,
		"last_updated_at": now.Format(time.RFC3339),
	}
	w := do(newRouter(h), req(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft", body))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotNil(t, resp["draft"])
}

func TestUpdateDraft_BindError(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	// send malformed (truncated) JSON — causes bind error
	r := httptest.NewRequest(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft", strings.NewReader("{bad json"))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", testTenantID.String())
	r.Header.Set("x-user-id", testUserID.String())
	w := do(newRouter(h), r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateDraft_InvalidUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodPut, "/api/v1/workflows/bad/draft", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateDraft_DraftConcurrency(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		update: func(_ context.Context, _, _, _ uuid.UUID, _ service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
			return nil, domain.ErrDraftConcurrency
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft", map[string]any{"name": "x"}))
	assert.Equal(t, http.StatusConflict, w.Code)
	var prob map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&prob))
	assert.Equal(t, "DRAFT_CONCURRENCY", prob["code"])
}

func TestDiscardDraft_OK(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		discard: func(_ context.Context, _, _ uuid.UUID) error { return nil },
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodDelete, "/api/v1/workflows/"+testWFID.String()+"/draft", nil))
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestDiscardDraft_InvalidUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodDelete, "/api/v1/workflows/bad/draft", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDiscardDraft_NoDraftExists(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		discard: func(_ context.Context, _, _ uuid.UUID) error { return domain.ErrNoDraftExists },
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodDelete, "/api/v1/workflows/"+testWFID.String()+"/draft", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}
