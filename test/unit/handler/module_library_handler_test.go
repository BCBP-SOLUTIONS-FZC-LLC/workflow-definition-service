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

func TestListModuleUsages_OK(t *testing.T) {
	two := int32(2)
	h := newModuleHandler(&fakeModuleSvc{
		usages: func(_ context.Context, _, moduleID uuid.UUID) ([]*domain.ModuleUsage, error) {
			assert.Equal(t, testWFID, moduleID)
			return []*domain.ModuleUsage{{
				WorkflowID: uuid.New(), WorkflowKey: "procurement", WorkflowName: "Procurement",
				VersionID: testVerID, VersionNumber: &two, Status: domain.VersionStatusPublished,
				ModuleVersionNumber: 3, Behind: true,
			}}, nil
		},
	})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules/"+testWFID.String()+"/usages", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Usages []map[string]any `json:"usages"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Usages, 1)
	u := resp.Usages[0]
	assert.Equal(t, "procurement", u["workflow_key"])
	assert.Equal(t, "Procurement", u["workflow_name"])
	assert.Equal(t, testVerID.String(), u["version_id"])
	assert.Equal(t, float64(2), u["version_number"])
	assert.Equal(t, "PUBLISHED", u["status"])
	assert.Equal(t, float64(3), u["module_version_number"])
	assert.Equal(t, true, u["behind"])
}

func TestListModuleUsages_NoneIsAnEmptyList(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		usages: func(context.Context, uuid.UUID, uuid.UUID) ([]*domain.ModuleUsage, error) { return nil, nil },
	})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules/"+testWFID.String()+"/usages", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"usages":[]}`, w.Body.String())
}

func TestListModuleUsages_Errors(t *testing.T) {
	notFound := newModuleHandler(&fakeModuleSvc{
		usages: func(context.Context, uuid.UUID, uuid.UUID) ([]*domain.ModuleUsage, error) {
			return nil, domain.ErrNotFound
		},
	})
	assert.Equal(t, http.StatusNotFound, do(newRouter(notFound), req(http.MethodGet, "/api/v1/modules/"+testWFID.String()+"/usages", nil)).Code)
	assert.Equal(t, http.StatusBadRequest, do(newRouter(notFound), req(http.MethodGet, "/api/v1/modules/bad/usages", nil)).Code)
	assert.Equal(t, http.StatusInternalServerError, do(newBareRouter(notFound), req(http.MethodGet, "/api/v1/modules/"+testWFID.String()+"/usages", nil)).Code)
}

func TestGetModule_ReturnsProcessID(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: testWFID, ProcessID: "Process_Review"},
				&domain.ModuleVersion{ID: testVerID, ProcessID: "Process_Review"}, nil
		},
	})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules/"+testWFID.String(), nil))
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		ProcessID     string `json:"process_id"`
		ActiveVersion struct {
			ProcessID string `json:"process_id"`
		} `json:"active_version"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "Process_Review", resp.ProcessID)
	assert.Equal(t, "Process_Review", resp.ActiveVersion.ProcessID)
}

func TestModuleProcessIDErrors_AreConflicts(t *testing.T) {
	for _, sentinel := range []error{domain.ErrModuleProcessIDTaken, domain.ErrModuleProcessIDChanged} {
		h := newModuleHandler(&fakeModuleSvc{
			create: func(context.Context, uuid.UUID, uuid.UUID, service.CreateModuleReq) (*domain.Module, *domain.ModuleVersion, error) {
				return nil, nil, sentinel
			},
			get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
				return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
			},
			addVersion: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.AddModuleVersionReq) (*domain.ModuleVersion, error) {
				return nil, sentinel
			},
		})
		w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/modules", map[string]any{"name": "m", "bpmn_xml": "<bpmn/>"}))
		assert.Equal(t, http.StatusConflict, w.Code, sentinel)
		assert.Contains(t, w.Body.String(), "MODULE_PROCESS_ID_CONFLICT")
		w = do(newRouter(h), adminReq(http.MethodPost, "/api/v1/modules/"+testWFID.String()+"/versions", map[string]any{"bpmn_xml": "<bpmn/>"}))
		assert.Equal(t, http.StatusConflict, w.Code, sentinel)
	}
}

func TestGetVersion_ReturnsModuleRefs(t *testing.T) {
	ver := newPublishedVersion()
	moduleID, versionID := uuid.New(), uuid.New()
	ver.ModuleRefs = []domain.ModuleRef{{ModuleID: moduleID, VersionID: versionID, ProcessID: "Process_Review", VersionNumber: 3}}
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
			return ver, nil
		},
	}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"/versions/"+testVerID.String(), nil))
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		ModuleRefs []map[string]any `json:"module_refs"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.ModuleRefs, 1)
	assert.Equal(t, map[string]any{
		"module_id": moduleID.String(), "version_id": versionID.String(),
		"process_id": "Process_Review", "version_number": float64(3),
	}, resp.ModuleRefs[0])
}

func TestGetVersion_NoModuleRefsIsAnEmptyList(t *testing.T) {
	ver := newPublishedVersion()
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
			return ver, nil
		},
	}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"/versions/"+testVerID.String(), nil))
	assert.Contains(t, w.Body.String(), `"module_refs":[]`)
}

func TestGetVersionValidation_ValidatesTheStoredVersion(t *testing.T) {
	ver := newPublishedVersion()
	var got *domain.WorkflowVersion
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
			return ver, nil
		},
	}, &fakeValidationSvc{
		validateVersion: func(_ context.Context, _ uuid.UUID, v *domain.WorkflowVersion) (bool, []domain.BPMNValidationError, error) {
			got = v
			return true, nil, nil
		},
	})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"/versions/"+testVerID.String()+"/validation", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Same(t, ver, got, "the stored version, with its status and module_refs, is what gets validated")
}
