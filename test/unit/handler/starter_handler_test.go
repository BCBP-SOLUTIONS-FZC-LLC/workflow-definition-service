package handler_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestListStarters_OK(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{
		list: func(context.Context, uuid.UUID, port.StarterFilter) ([]*domain.StarterTemplate, int64, error) {
			return []*domain.StarterTemplate{{ID: testWFID}}, 1, nil
		},
	})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/starters", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListStarters_InvalidScope(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/starters?scope=bogus", nil))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestListStarters_MissingCtx(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{})
	w := do(newBareRouter(h), req(http.MethodGet, "/api/v1/starters", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListStarters_Error(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{
		list: func(context.Context, uuid.UUID, port.StarterFilter) ([]*domain.StarterTemplate, int64, error) {
			return nil, 0, errors.New("db error")
		},
	})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/starters", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreateStarter_TenantScope_OK(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{
		create: func(_ context.Context, _, _ uuid.UUID, req service.CreateStarterReq) (*domain.StarterTemplate, error) {
			assert.Equal(t, domain.ScopeTenant, req.Scope)
			return &domain.StarterTemplate{ID: testWFID}, nil
		},
	})
	body := map[string]any{"name": "s", "bpmn_xml": "<bpmn/>"}
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/starters", body))
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestCreateStarter_GlobalScope_WithPlatformOperator_OK(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{
		create: func(_ context.Context, _, _ uuid.UUID, req service.CreateStarterReq) (*domain.StarterTemplate, error) {
			assert.Equal(t, domain.ScopeGlobal, req.Scope)
			return &domain.StarterTemplate{ID: testWFID}, nil
		},
	})
	body := map[string]any{"name": "s", "bpmn_xml": "<bpmn/>", "scope": "global"}
	w := do(newRouter(h), platformOperatorReq(http.MethodPost, "/api/v1/starters", body))
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestCreateStarter_GlobalScope_ForbiddenWithoutPlatformOperator(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{})
	body := map[string]any{"name": "s", "bpmn_xml": "<bpmn/>", "scope": "global"}
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/starters", body))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCreateStarter_InvalidScope(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{})
	body := map[string]any{"name": "s", "bpmn_xml": "<bpmn/>", "scope": "bogus"}
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/starters", body))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestCreateStarter_NotAdmin(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{})
	body := map[string]any{"name": "s", "bpmn_xml": "<bpmn/>"}
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/starters", body))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCreateStarter_BindError(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{})
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/starters", map[string]any{}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateStarter_ServiceError(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{
		create: func(context.Context, uuid.UUID, uuid.UUID, service.CreateStarterReq) (*domain.StarterTemplate, error) {
			return nil, errors.New("db error")
		},
	})
	body := map[string]any{"name": "s", "bpmn_xml": "<bpmn/>"}
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/starters", body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreateStarterFromWorkflowVersion_OK(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{
		createFromWorkflowVersion: func(_ context.Context, _, _, sourceVersionID uuid.UUID, req service.CreateFromWorkflowVersionReq) (*domain.StarterTemplate, error) {
			assert.Equal(t, testVerID, sourceVersionID)
			assert.Equal(t, "Onboarding", req.Name)
			return &domain.StarterTemplate{ID: testWFID}, nil
		},
	})
	body := map[string]any{"name": "Onboarding"}
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/starters/from-workflow-version/"+testVerID.String(), body))
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestCreateStarterFromWorkflowVersion_NotAdmin(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{})
	body := map[string]any{"name": "s"}
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/starters/from-workflow-version/"+testVerID.String(), body))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCreateStarterFromWorkflowVersion_BindError(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{})
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/starters/from-workflow-version/"+testVerID.String(), map[string]any{}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateStarterFromWorkflowVersion_ServiceError(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{
		createFromWorkflowVersion: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.CreateFromWorkflowVersionReq) (*domain.StarterTemplate, error) {
			return nil, domain.ErrVersionNotPublished
		},
	})
	body := map[string]any{"name": "s"}
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/starters/from-workflow-version/"+testVerID.String(), body))
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestGetStarter_OK(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.StarterTemplate, error) {
			return &domain.StarterTemplate{ID: testWFID}, nil
		},
	})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/starters/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetStarter_MissingCtx(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{})
	w := do(newBareRouter(h), req(http.MethodGet, "/api/v1/starters/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetStarter_InvalidUUID(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/starters/not-a-uuid", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetStarter_NotFound(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.StarterTemplate, error) {
			return nil, domain.ErrNotFound
		},
	})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/starters/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDeleteStarter_TenantScope_OK(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.StarterTemplate, error) {
			return &domain.StarterTemplate{ID: testWFID, Scope: domain.ScopeTenant}, nil
		},
		del: func(context.Context, uuid.UUID, uuid.UUID) error { return nil },
	})
	w := do(newRouter(h), adminReq(http.MethodDelete, "/api/v1/starters/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestDeleteStarter_NotAdmin(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{})
	w := do(newRouter(h), req(http.MethodDelete, "/api/v1/starters/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestDeleteStarter_GetError(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.StarterTemplate, error) {
			return nil, domain.ErrNotFound
		},
	})
	w := do(newRouter(h), adminReq(http.MethodDelete, "/api/v1/starters/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDeleteStarter_GlobalScope_ForbiddenWithoutPlatformOperator(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.StarterTemplate, error) {
			return &domain.StarterTemplate{ID: testWFID, Scope: domain.ScopeGlobal}, nil
		},
	})
	w := do(newRouter(h), adminReq(http.MethodDelete, "/api/v1/starters/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestDeleteStarter_GlobalScope_WithPlatformOperator_OK(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.StarterTemplate, error) {
			return &domain.StarterTemplate{ID: testWFID, Scope: domain.ScopeGlobal}, nil
		},
		del: func(context.Context, uuid.UUID, uuid.UUID) error { return nil },
	})
	w := do(newRouter(h), platformOperatorReq(http.MethodDelete, "/api/v1/starters/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestDeleteStarter_ServiceError(t *testing.T) {
	h := newStarterHandler(&fakeStarterSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.StarterTemplate, error) {
			return &domain.StarterTemplate{ID: testWFID, Scope: domain.ScopeTenant}, nil
		},
		del: func(context.Context, uuid.UUID, uuid.UUID) error { return domain.ErrNotFound },
	})
	w := do(newRouter(h), adminReq(http.MethodDelete, "/api/v1/starters/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}
