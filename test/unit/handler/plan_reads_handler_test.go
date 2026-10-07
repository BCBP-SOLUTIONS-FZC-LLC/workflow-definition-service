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

var nodesPath = "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/nodes"

func TestGetVersionNodes_OK(t *testing.T) {
	n := int32(3)
	vs := &fakeVersionSvc{nodes: func(_ context.Context, tenantID, workflowID, versionID uuid.UUID) (*service.VersionNodes, error) {
		assert.Equal(t, testTenantID, tenantID)
		assert.Equal(t, testWFID, workflowID)
		assert.Equal(t, testVerID, versionID)
		return &service.VersionNodes{
			Version: &domain.WorkflowVersion{ID: testVerID, WorkflowID: testWFID, VersionNumber: &n},
			Nodes: []service.PlanNode{{
				Key: "CA_Eng::Process_Review/Review_Task", NodeID: "Review_Task", Name: "Review", StageType: "review", Role: "reviewer",
				DepartmentID: "CA_Eng::Process_Review", DepartmentLabel: "Engineering", IAMDepartmentID: "2200a5ae-2466-54f7-a1d8-243106e05705",
				DefaultUserIDs: []string{"018f2d3c-0000-7000-8000-0000000000a1"},
			}},
			Waits: []service.PlanWait{
				{Key: "Ops/Receive_Bid", Kind: service.WaitReceiveTask, Message: "bid-received"},
				{Key: "Ops/Prepare#Boundary_Withdraw", Kind: service.WaitMessageBoundary, Message: "withdraw"},
			},
		}, nil
	}}
	w := do(newRouter(newHandler(nil, nil, vs, nil)), req(http.MethodGet, nodesPath, nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{
	  "workflow_id": "`+testWFID.String()+`", "version_id": "`+testVerID.String()+`", "version_number": 3,
	  "nodes": [{
	    "node_key": "CA_Eng::Process_Review/Review_Task", "node_id": "Review_Task", "name": "Review",
	    "stage_type": "review", "role_level": "reviewer",
	    "department": {"id": "CA_Eng::Process_Review", "label": "Engineering", "iam_department_id": "2200a5ae-2466-54f7-a1d8-243106e05705"},
	    "default_user_ids": ["018f2d3c-0000-7000-8000-0000000000a1"]
	  }],
	  "waits": [
	    {"key": "Ops/Receive_Bid", "kind": "receive_task", "message": "bid-received"},
	    {"key": "Ops/Prepare#Boundary_Withdraw", "kind": "message_boundary", "message": "withdraw"}
	  ]
	}`, w.Body.String())
}

func TestGetVersionNodes_NoNodesIsAnEmptyList(t *testing.T) {
	n := int32(1)
	vs := &fakeVersionSvc{nodes: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*service.VersionNodes, error) {
		return &service.VersionNodes{Version: &domain.WorkflowVersion{ID: testVerID, WorkflowID: testWFID, VersionNumber: &n}}, nil
	}}
	w := do(newRouter(newHandler(nil, nil, vs, nil)), req(http.MethodGet, nodesPath, nil))
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, []any{}, body["nodes"])
	assert.Equal(t, []any{}, body["waits"])
}

func TestGetVersionNodes_Errors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"draft", domain.ErrVersionNotPublished, http.StatusConflict},
		{"not found", domain.ErrNotFound, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vs := &fakeVersionSvc{nodes: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*service.VersionNodes, error) {
				return nil, tc.err
			}}
			w := do(newRouter(newHandler(nil, nil, vs, nil)), req(http.MethodGet, nodesPath, nil))
			assert.Equal(t, tc.want, w.Code)
		})
	}
}

func TestGetVersionNodes_BadID(t *testing.T) {
	w := do(newRouter(newHandler(nil, nil, &fakeVersionSvc{}, nil)),
		req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"/versions/not-a-uuid/nodes", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetVersionNodes_MissingCtx(t *testing.T) {
	w := do(newBareRouter(newHandler(nil, nil, &fakeVersionSvc{}, nil)), req(http.MethodGet, nodesPath, nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

var contractPath = "/api/v1/modules/" + testWFID.String() + "/versions/" + testVerID.String() + "/contract"

func TestGetModuleContract_OK(t *testing.T) {
	n := int32(2)
	mod := &fakeModuleSvc{contract: func(_ context.Context, tenantID, moduleID, versionID uuid.UUID) (*service.ModuleContract, error) {
		assert.Equal(t, testTenantID, tenantID)
		assert.Equal(t, testWFID, moduleID)
		assert.Equal(t, testVerID, versionID)
		return &service.ModuleContract{
			Version: &domain.ModuleVersion{ID: testVerID, ModuleID: testWFID, ProcessID: "Process_Package", VersionNumber: &n},
			Slots: []service.ModuleSlot{
				{ID: "Author", Label: "Author", Open: true},
				{ID: "Quality", Label: "Quality", IAMDepartmentID: "6f1c0e52-3b1d-5d7e-9a3c-0c4b8e2f1a77"},
			},
			Tasks: []service.ModuleTask{
				{AssigneeKey: "Pkg_Check::Check_Task", NodeID: "Check_Task", Name: "Check", Slot: "Author", StageType: "review", Role: "checker"},
			},
			Messages:   []string{"package-withdrawn"},
			ErrorCodes: []string{"PACKAGE_REJECTED"},
		}, nil
	}}
	w := do(newRouter(newModuleHandler(mod)), req(http.MethodGet, contractPath, nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{
	  "module_id": "`+testWFID.String()+`", "version_id": "`+testVerID.String()+`",
	  "process_id": "Process_Package", "version_number": 2,
	  "slots": [
	    {"id": "Author", "label": "Author", "open": true},
	    {"id": "Quality", "label": "Quality", "open": false, "iam_department_id": "6f1c0e52-3b1d-5d7e-9a3c-0c4b8e2f1a77"}
	  ],
	  "tasks": [{
	    "assignee_key": "Pkg_Check::Check_Task", "node_id": "Check_Task", "name": "Check", "slot": "Author",
	    "stage_type": "review", "role_level": "checker", "default_user_ids": []
	  }],
	  "messages": ["package-withdrawn"], "error_codes": ["PACKAGE_REJECTED"], "names_users": false
	}`, w.Body.String())
}

func TestGetModuleContract_DraftHasNoVersionNumber(t *testing.T) {
	mod := &fakeModuleSvc{contract: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*service.ModuleContract, error) {
		return &service.ModuleContract{Version: &domain.ModuleVersion{ID: testVerID, ModuleID: testWFID, ProcessID: "Process_Package"}}, nil
	}}
	w := do(newRouter(newModuleHandler(mod)), req(http.MethodGet, contractPath, nil))
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.NotContains(t, body, "version_number")
	assert.Equal(t, []any{}, body["slots"])
	assert.Equal(t, []any{}, body["tasks"])
	assert.Equal(t, []any{}, body["messages"])
	assert.Equal(t, []any{}, body["error_codes"])
}

func TestGetModuleContract_Errors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"not found", domain.ErrNotFound, http.StatusNotFound},
		{"no longer compiles", &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{Code: domain.BPMNErrUnresolvedCalledElement}}}, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mod := &fakeModuleSvc{contract: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*service.ModuleContract, error) {
				return nil, tc.err
			}}
			w := do(newRouter(newModuleHandler(mod)), req(http.MethodGet, contractPath, nil))
			assert.Equal(t, tc.want, w.Code)
		})
	}
}

func TestGetModuleContract_BadID(t *testing.T) {
	w := do(newRouter(newModuleHandler(&fakeModuleSvc{})),
		req(http.MethodGet, "/api/v1/modules/not-a-uuid/versions/"+testVerID.String()+"/contract", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetModuleContract_MissingCtx(t *testing.T) {
	w := do(newBareRouter(newModuleHandler(&fakeModuleSvc{})), req(http.MethodGet, contractPath, nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
