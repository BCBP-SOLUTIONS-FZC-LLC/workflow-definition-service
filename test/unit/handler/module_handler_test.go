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
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestListModules_OK(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		list: func(_ context.Context, _ uuid.UUID, filter port.ModuleFilter) ([]*domain.Module, int64, error) {
			assert.Equal(t, domain.ScopeGlobal, *filter.Scope)
			assert.Equal(t, "approval", *filter.Search)
			return []*domain.Module{{ID: testWFID}}, 1, nil
		},
	})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules?scope=global&q=approval", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Len(t, resp["modules"].([]any), 1)
}

func TestListModules_InvalidScope(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules?scope=bogus", nil))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestListModules_MissingCtx(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{})
	w := do(newBareRouter(h), req(http.MethodGet, "/api/v1/modules", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListModules_Error(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		list: func(context.Context, uuid.UUID, port.ModuleFilter) ([]*domain.Module, int64, error) {
			return nil, 0, errors.New("db error")
		},
	})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreateModule_TenantScope_OK(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		create: func(_ context.Context, _, _ uuid.UUID, req service.CreateModuleReq) (*domain.Module, *domain.ModuleVersion, error) {
			assert.Equal(t, domain.ScopeTenant, req.Scope)
			return &domain.Module{ID: testWFID}, &domain.ModuleVersion{ID: testVerID, Status: domain.VersionStatusDraft}, nil
		},
	})
	body := map[string]any{"name": "m", "bpmn_xml": "<bpmn/>"}
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/modules", body))
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestCreateModule_GlobalScope_WithPlatformOperator_OK(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		create: func(_ context.Context, _, _ uuid.UUID, req service.CreateModuleReq) (*domain.Module, *domain.ModuleVersion, error) {
			assert.Equal(t, domain.ScopeGlobal, req.Scope)
			return &domain.Module{ID: testWFID}, &domain.ModuleVersion{ID: testVerID}, nil
		},
	})
	body := map[string]any{"name": "m", "bpmn_xml": "<bpmn/>", "scope": "global"}
	w := do(newRouter(h), platformOperatorReq(http.MethodPost, "/api/v1/modules", body))
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestCreateModule_GlobalScope_ForbiddenWithoutPlatformOperator(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{})
	body := map[string]any{"name": "m", "bpmn_xml": "<bpmn/>", "scope": "global"}
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/modules", body))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCreateModule_InvalidScope(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{})
	body := map[string]any{"name": "m", "bpmn_xml": "<bpmn/>", "scope": "bogus"}
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/modules", body))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestCreateModule_NotAdmin(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{})
	body := map[string]any{"name": "m", "bpmn_xml": "<bpmn/>"}
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/modules", body))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCreateModule_BindError(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{})
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/modules", map[string]any{}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateModule_ServiceError(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		create: func(context.Context, uuid.UUID, uuid.UUID, service.CreateModuleReq) (*domain.Module, *domain.ModuleVersion, error) {
			return nil, nil, domain.ErrPlanQuotaExceeded
		},
	})
	body := map[string]any{"name": "m", "bpmn_xml": "<bpmn/>"}
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/modules", body))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGetModule_OK_WithActiveVersion(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: testWFID}, &domain.ModuleVersion{ID: testVerID, BPMNXML: "<bpmn/>"}, nil
		},
	})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotNil(t, resp["active_version"])
}

func TestGetModule_OK_NoActiveVersion(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: testWFID}, nil, nil
		},
	})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Nil(t, resp["active_version"])
}

func TestGetModule_MissingCtx(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{})
	w := do(newBareRouter(h), req(http.MethodGet, "/api/v1/modules/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetModule_InvalidUUID(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules/not-a-uuid", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetModule_NotFound(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return nil, nil, domain.ErrNotFound
		},
	})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestListModuleVersions_OK(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		listVersions: func(context.Context, uuid.UUID, uuid.UUID, int, int) ([]*domain.ModuleVersion, int64, error) {
			return []*domain.ModuleVersion{{ID: testVerID}}, 1, nil
		},
	})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules/"+testWFID.String()+"/versions", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListModuleVersions_MissingCtx(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{})
	w := do(newBareRouter(h), req(http.MethodGet, "/api/v1/modules/"+testWFID.String()+"/versions", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListModuleVersions_Error(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		listVersions: func(context.Context, uuid.UUID, uuid.UUID, int, int) ([]*domain.ModuleVersion, int64, error) {
			return nil, 0, errors.New("db error")
		},
	})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules/"+testWFID.String()+"/versions", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetModuleVersion_OK(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		getVersion: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.ModuleVersion, error) {
			return &domain.ModuleVersion{ID: testVerID}, nil
		},
	})
	path := "/api/v1/modules/" + testWFID.String() + "/versions/" + testVerID.String()
	w := do(newRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetModuleVersion_MissingCtx(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{})
	path := "/api/v1/modules/" + testWFID.String() + "/versions/" + testVerID.String()
	w := do(newBareRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetModuleVersion_NotFound(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		getVersion: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.ModuleVersion, error) {
			return nil, domain.ErrNotFound
		},
	})
	path := "/api/v1/modules/" + testWFID.String() + "/versions/" + testVerID.String()
	w := do(newRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAddModuleVersion_ExplicitBody_OK(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
		},
		addVersion: func(_ context.Context, _, _, _ uuid.UUID, req service.AddModuleVersionReq) (*domain.ModuleVersion, error) {
			require.NotNil(t, req.BPMNXML)
			assert.Equal(t, "<new/>", *req.BPMNXML)
			return &domain.ModuleVersion{ID: testVerID2}, nil
		},
	})
	body := map[string]any{"bpmn_xml": "<new/>"}
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/modules/"+testWFID.String()+"/versions", body))
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestAddModuleVersion_EmptyBody_CopiesForward(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
		},
		addVersion: func(_ context.Context, _, _, _ uuid.UUID, req service.AddModuleVersionReq) (*domain.ModuleVersion, error) {
			assert.Nil(t, req.BPMNXML)
			return &domain.ModuleVersion{ID: testVerID2}, nil
		},
	})
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/modules/"+testWFID.String()+"/versions", nil))
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestAddModuleVersion_NotAdmin(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{})
	w := do(newRouter(h), req(http.MethodPost, "/api/v1/modules/"+testWFID.String()+"/versions", nil))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAddModuleVersion_GetModuleError(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return nil, nil, domain.ErrNotFound
		},
	})
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/modules/"+testWFID.String()+"/versions", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAddModuleVersion_GlobalScope_ForbiddenWithoutPlatformOperator(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: testWFID, Scope: domain.ScopeGlobal}, nil, nil
		},
	})
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/modules/"+testWFID.String()+"/versions", nil))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAddModuleVersion_GlobalScope_WithPlatformOperator_OK(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: testWFID, Scope: domain.ScopeGlobal}, nil, nil
		},
		addVersion: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.AddModuleVersionReq) (*domain.ModuleVersion, error) {
			return &domain.ModuleVersion{ID: testVerID2}, nil
		},
	})
	w := do(newRouter(h), platformOperatorReq(http.MethodPost, "/api/v1/modules/"+testWFID.String()+"/versions", nil))
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestAddModuleVersion_ServiceError(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
		},
		addVersion: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.AddModuleVersionReq) (*domain.ModuleVersion, error) {
			return nil, domain.ErrNoActiveVersion
		},
	})
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/modules/"+testWFID.String()+"/versions", nil))
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestPublishModuleVersion_OK(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
		},
		publish: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.ModuleVersion, error) {
			n := int32(1)
			return &domain.ModuleVersion{ID: testVerID, Status: domain.VersionStatusPublished, VersionNumber: &n}, nil
		},
	})
	path := "/api/v1/modules/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), adminReq(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPublishModuleVersion_NotAdmin(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{})
	path := "/api/v1/modules/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), req(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPublishModuleVersion_GetError(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return nil, nil, domain.ErrNotFound
		},
	})
	path := "/api/v1/modules/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), adminReq(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPublishModuleVersion_GlobalScope_ForbiddenWithoutPlatformOperator(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: testWFID, Scope: domain.ScopeGlobal}, nil, nil
		},
	})
	path := "/api/v1/modules/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), adminReq(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPublishModuleVersion_GlobalScope_WithPlatformOperator_OK(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: testWFID, Scope: domain.ScopeGlobal}, nil, nil
		},
		publish: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.ModuleVersion, error) {
			n := int32(1)
			return &domain.ModuleVersion{ID: testVerID, Status: domain.VersionStatusPublished, VersionNumber: &n}, nil
		},
	})
	path := "/api/v1/modules/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), platformOperatorReq(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPublishModuleVersion_Error(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
		},
		publish: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.ModuleVersion, error) {
			return nil, domain.ErrInvalidVersionStatus
		},
	})
	path := "/api/v1/modules/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newRouter(h), adminReq(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestArchiveModule_OK(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
		},
		archive: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error { return nil },
	})
	w := do(newRouter(h), adminReq(http.MethodDelete, "/api/v1/modules/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestArchiveModule_NotAdmin(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{})
	w := do(newRouter(h), req(http.MethodDelete, "/api/v1/modules/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestArchiveModule_GetModuleError(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return nil, nil, domain.ErrNotFound
		},
	})
	w := do(newRouter(h), adminReq(http.MethodDelete, "/api/v1/modules/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestArchiveModule_GlobalScope_ForbiddenWithoutPlatformOperator(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: testWFID, Scope: domain.ScopeGlobal}, nil, nil
		},
	})
	w := do(newRouter(h), adminReq(http.MethodDelete, "/api/v1/modules/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestArchiveModule_GlobalScope_WithPlatformOperator_OK(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: testWFID, Scope: domain.ScopeGlobal}, nil, nil
		},
		archive: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error { return nil },
	})
	w := do(newRouter(h), platformOperatorReq(http.MethodDelete, "/api/v1/modules/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestArchiveModule_ServiceError(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
		},
		archive: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error { return domain.ErrNoActiveVersion },
	})
	w := do(newRouter(h), adminReq(http.MethodDelete, "/api/v1/modules/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusConflict, w.Code)
}
