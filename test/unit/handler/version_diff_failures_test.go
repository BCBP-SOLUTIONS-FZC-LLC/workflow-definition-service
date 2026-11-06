package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const (
	diffFailureBaseXML          = "<base-definitions/>"
	diffFailureTargetXML        = "<target-definitions/>"
	diffFailureNoZeebeNamespace = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="urn:t">
  <bpmn:process id="Process_NoLanes" isExecutable="true">
    <bpmn:startEvent id="Start"/>
    <bpmn:userTask id="Task_A"/>
    <bpmn:endEvent id="End"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start" targetRef="Task_A"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_A" targetRef="End"/>
  </bpmn:process>
</bpmn:definitions>`
)

type diffFailureDeps struct {
	versions *mocks.MockWorkflowVersionRepository
	modules  *mocks.MockModuleVersionRepository
	compiler *mocks.MockPlanCompiler
}

func newDiffFailureRouter(t *testing.T, realCompiler bool) (*diffFailureDeps, *gin.Engine) {
	t.Helper()
	ctrl := gomock.NewController(t)
	d := &diffFailureDeps{
		versions: mocks.NewMockWorkflowVersionRepository(ctrl),
		modules:  mocks.NewMockModuleVersionRepository(ctrl),
		compiler: mocks.NewMockPlanCompiler(ctrl),
	}
	deps := service.VersionDeps{Versions: d.versions, ModuleVersions: d.modules}
	if realCompiler {
		deps.Compiler = bpmn_compiler.New()
	} else {
		deps.Compiler = d.compiler
	}
	svc := service.NewVersionService(deps)
	return d, newRouter(handler.New(handler.Services{Versions: svc}))
}

func diffFailurePath(baseID, targetID uuid.UUID) string {
	return "/api/v1/workflows/" + testWFID.String() + "/versions/" + baseID.String() + "/diff/" + targetID.String()
}

func diffFailureVersion(id uuid.UUID, status domain.VersionStatus, xml string) *domain.WorkflowVersion {
	v := &domain.WorkflowVersion{ID: id, WorkflowID: testWFID, TenantID: testTenantID, Status: status, BPMNXML: xml}
	if status != domain.VersionStatusDraft {
		n := int32(1)
		v.VersionNumber = &n
	}
	return v
}

func diffFailureStoredPlan(t *testing.T, deptIDs ...string) *string {
	t.Helper()
	depts := make([]dsl.DepartmentDef, 0, len(deptIDs))
	for _, id := range deptIDs {
		depts = append(depts, dsl.DepartmentDef{ID: id, Label: id})
	}
	plan := &dsl.CompiledPlan{Name: "stored", Departments: depts}
	b, err := json.Marshal(&dsl.CompiledCollaboration{
		MainPlan: plan.Name, Plans: []*dsl.CompiledPlan{plan}, SchemaVersion: dsl.CurrentSchemaVersion,
	})
	require.NoError(t, err)
	stored := string(b)
	return &stored
}

func TestGetVersionDiff_VersionWithoutAUsablePlan_ReturnsAValidationProblem(t *testing.T) {
	t.Parallel()
	callIssue := domain.BPMNValidationError{
		Code: domain.BPMNErrorCode("CALL_TARGET_INVALID"), NodeID: "CA_9",
		Message: "call is missing its called element", Severity: domain.SeverityError,
	}
	cases := []struct {
		name        string
		status      domain.VersionStatus
		moduleCalls []domain.ModuleCall
		callIssues  []domain.BPMNValidationError
		snapshot    []domain.ModuleRef
		wantParams  []map[string]any
	}{
		{
			name:        "published version calls a module missing from its pinned snapshot",
			status:      domain.VersionStatusPublished,
			moduleCalls: []domain.ModuleCall{{CallID: "CA_1", ProcessID: "Process_Review"}},
			wantParams: []map[string]any{{
				"name": "CA_1", "code": "UNRESOLVED_CALLED_ELEMENT",
				"reason": `call names module "Process_Review", which is not a published, unarchived module this tenant can call`,
			}},
		},
		{
			name:        "archived version pins a module number its snapshot does not hold",
			status:      domain.VersionStatusArchived,
			moduleCalls: []domain.ModuleCall{{CallID: "CA_2", ProcessID: "Process_Review", VersionNumber: 7}},
			snapshot:    []domain.ModuleRef{{ModuleID: uuid.New(), VersionID: uuid.New(), ProcessID: "Process_Review", VersionNumber: 3}},
			wantParams: []map[string]any{{
				"name": "CA_2", "code": "UNRESOLVED_CALLED_ELEMENT",
				"reason": `call names module "Process_Review" version 7, which does not exist or was never published, or the module is archived`,
			}},
		},
		{
			name:       "version whose module calls carry their own issue",
			status:     domain.VersionStatusDraft,
			callIssues: []domain.BPMNValidationError{callIssue},
			wantParams: []map[string]any{{
				"name": "CA_9", "code": "CALL_TARGET_INVALID", "reason": "call is missing its called element",
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, router := newDiffFailureRouter(t, false)
			baseID, targetID := uuid.New(), uuid.New()
			base := diffFailureVersion(baseID, tc.status, diffFailureBaseXML)
			base.ModuleRefs = tc.snapshot
			d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, baseID).Return(base, nil)
			d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, targetID).
				Return(diffFailureVersion(targetID, domain.VersionStatusDraft, diffFailureTargetXML), nil)
			d.compiler.EXPECT().ModuleCalls(gomock.Any(), diffFailureBaseXML).Return(tc.moduleCalls, tc.callIssues, nil)

			w := do(router, req(http.MethodGet, diffFailurePath(baseID, targetID), nil))

			require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
			assert.Equal(t, "BPMN semantic or structural validation failed.", prob["detail"])
			params, ok := prob["invalid_params"].([]any)
			require.True(t, ok, "invalid_params missing: %s", w.Body.String())
			require.Len(t, params, len(tc.wantParams))
			for i, want := range tc.wantParams {
				assert.Equal(t, want, params[i])
			}
		})
	}
}

func TestGetVersionDiff_BpmnThatFailsCompilation_ReturnsAValidationProblemPerIssue(t *testing.T) {
	t.Parallel()
	d, router := newDiffFailureRouter(t, true)
	baseID, targetID := uuid.New(), uuid.New()
	d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, baseID).
		Return(diffFailureVersion(baseID, domain.VersionStatusDraft, diffFailureNoZeebeNamespace), nil)
	d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, targetID).
		Return(diffFailureVersion(targetID, domain.VersionStatusDraft, diffFailureNoZeebeNamespace), nil)

	w := do(router, req(http.MethodGet, diffFailurePath(baseID, targetID), nil))

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	prob := decodeProblem(t, w)
	assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
	params, ok := prob["invalid_params"].([]any)
	require.True(t, ok, "invalid_params missing: %s", w.Body.String())
	require.NotEmpty(t, params)
	first, ok := params[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "MISSING_NAMESPACE", first["code"])
	assert.Contains(t, first["reason"], "Zeebe namespace")
}

func TestGetVersionDiff_CompiledMainPlanMissing_Returns500WithoutLeakingTheCause(t *testing.T) {
	t.Parallel()
	d, router := newDiffFailureRouter(t, false)
	baseID, targetID := uuid.New(), uuid.New()
	d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, baseID).
		Return(diffFailureVersion(baseID, domain.VersionStatusDraft, diffFailureBaseXML), nil)
	d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, targetID).
		Return(diffFailureVersion(targetID, domain.VersionStatusDraft, diffFailureTargetXML), nil)
	d.compiler.EXPECT().ModuleCalls(gomock.Any(), diffFailureBaseXML).Return(nil, nil, nil)
	d.compiler.EXPECT().BundleModules(diffFailureBaseXML, gomock.Any()).Return(diffFailureBaseXML, nil)
	d.compiler.EXPECT().CompileDraft(gomock.Any(), diffFailureBaseXML).Return(&dsl.CompiledCollaboration{
		MainPlan:      "Process_Main",
		Plans:         []*dsl.CompiledPlan{{Name: "Process_Other"}},
		SchemaVersion: dsl.CurrentSchemaVersion,
	}, nil)

	w := do(router, req(http.MethodGet, diffFailurePath(baseID, targetID), nil))

	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	prob := decodeProblem(t, w)
	assert.Equal(t, "INTERNAL_ERROR", prob["code"])
	assert.Equal(t, "an unexpected error occurred", prob["detail"])
	assert.NotContains(t, w.Body.String(), "Process_Main")
}

func TestGetVersionDiff_DatabaseUnavailable_Returns503(t *testing.T) {
	t.Parallel()
	outage := fmt.Errorf("query: %w: connection refused", domain.ErrUpstreamUnavailable)
	reviewVersionID := uuid.New()
	cases := []struct {
		name    string
		arrange func(d *diffFailureDeps, baseID, targetID uuid.UUID)
	}{
		{
			name: "base version lookup fails",
			arrange: func(d *diffFailureDeps, baseID, _ uuid.UUID) {
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, baseID).Return(nil, outage)
			},
		},
		{
			name: "target version lookup fails",
			arrange: func(d *diffFailureDeps, baseID, targetID uuid.UUID) {
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, baseID).
					Return(diffFailureVersion(baseID, domain.VersionStatusDraft, diffFailureBaseXML), nil)
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, targetID).Return(nil, outage)
			},
		},
		{
			name: "live module lookup fails for a draft",
			arrange: func(d *diffFailureDeps, baseID, targetID uuid.UUID) {
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, baseID).
					Return(diffFailureVersion(baseID, domain.VersionStatusDraft, diffFailureBaseXML), nil)
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, targetID).
					Return(diffFailureVersion(targetID, domain.VersionStatusDraft, diffFailureTargetXML), nil)
				d.compiler.EXPECT().ModuleCalls(gomock.Any(), diffFailureBaseXML).
					Return([]domain.ModuleCall{{CallID: "CA_1", ProcessID: "Process_Review"}}, nil, nil)
				d.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Review", int32(0)).Return(nil, outage)
			},
		},
		{
			name: "pinned module version lookup fails for a published version",
			arrange: func(d *diffFailureDeps, baseID, targetID uuid.UUID) {
				base := diffFailureVersion(baseID, domain.VersionStatusPublished, diffFailureBaseXML)
				base.ModuleRefs = []domain.ModuleRef{{
					ModuleID: uuid.New(), VersionID: reviewVersionID, ProcessID: "Process_Review", VersionNumber: 2,
				}}
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, baseID).Return(base, nil)
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, targetID).
					Return(diffFailureVersion(targetID, domain.VersionStatusDraft, diffFailureTargetXML), nil)
				d.compiler.EXPECT().ModuleCalls(gomock.Any(), diffFailureBaseXML).
					Return([]domain.ModuleCall{{CallID: "CA_1", ProcessID: "Process_Review"}}, nil, nil)
				d.modules.EXPECT().GetByID(gomock.Any(), testTenantID, reviewVersionID).Return(nil, outage)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, router := newDiffFailureRouter(t, false)
			baseID, targetID := uuid.New(), uuid.New()
			tc.arrange(d, baseID, targetID)

			w := do(router, req(http.MethodGet, diffFailurePath(baseID, targetID), nil))

			require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "UPSTREAM_UNAVAILABLE", prob["code"])
			assert.Equal(t, "an upstream dependency is temporarily unavailable", prob["detail"])
			assert.NotContains(t, w.Body.String(), "connection refused", "driver text stays out of the response")
			assert.NotContains(t, w.Body.String(), "invalid_params", "an outage is not a validation problem")
		})
	}
}

func TestGetVersionDiff_VersionComparedWithItself_Returns200MetadataOnly(t *testing.T) {
	t.Parallel()
	d, router := newDiffFailureRouter(t, false)
	versionID := uuid.New()
	v := diffFailureVersion(versionID, domain.VersionStatusPublished, diffFailureBaseXML)
	v.CompiledPlanJSON = diffFailureStoredPlan(t, "finance", "operations")
	d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, versionID).Return(v, nil).Times(2)

	w := do(router, req(http.MethodGet, diffFailurePath(versionID, versionID), nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeProblem(t, w)
	assert.Equal(t, versionID.String(), body["base_version_id"])
	assert.Equal(t, versionID.String(), body["target_version_id"])
	assert.Equal(t, "METADATA_ONLY", body["change_type"])
	assert.Equal(t, map[string]any{
		"added_departments":   []any{},
		"removed_departments": []any{},
		"step_changes":        []any{},
		"stage_changes":       []any{},
		"metadata_only":       true,
	}, body["changes"])
}
