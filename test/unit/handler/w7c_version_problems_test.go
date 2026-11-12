package handler_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const w7cMalformedXML = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"><bpmn:process id="Process_Open">`

type w7cDeps struct {
	versions  *mocks.MockWorkflowVersionRepository
	workflows *mocks.MockWorkflowRepository
	modules   *mocks.MockModuleVersionRepository
	compiler  *mocks.MockPlanCompiler
}

func w7cRouter(t *testing.T) (*w7cDeps, *gin.Engine) {
	t.Helper()
	ctrl := gomock.NewController(t)
	d := &w7cDeps{
		versions:  mocks.NewMockWorkflowVersionRepository(ctrl),
		workflows: mocks.NewMockWorkflowRepository(ctrl),
		modules:   mocks.NewMockModuleVersionRepository(ctrl),
		compiler:  mocks.NewMockPlanCompiler(ctrl),
	}
	versionSvc := service.NewVersionService(service.VersionDeps{
		Transactor: mocks.NewMockTransactor(ctrl), Workflows: d.workflows, Versions: d.versions,
		Compiler: d.compiler, ModuleVersions: d.modules,
	})
	validationSvc := service.NewValidationService(service.ValidationDeps{Compiler: d.compiler, ModuleVersions: d.modules})
	return d, newRouter(handler.New(handler.Services{Versions: versionSvc, Validation: validationSvc}))
}

func w7cVersion(status domain.VersionStatus, xml string) *domain.WorkflowVersion {
	v := &domain.WorkflowVersion{ID: testVerID, WorkflowID: testWFID, TenantID: testTenantID, Status: status, BPMNXML: xml, IsValid: true}
	if status != domain.VersionStatusDraft {
		n := int32(2)
		v.VersionNumber = &n
	}
	return v
}

func w7cPromotePath() string {
	return "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/promote"
}

func TestPromoteVersion_AVersionOrWorkflowThatCannotBePromoted_AnswersTheMatchingProblem(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		arrange    func(d *w7cDeps)
		wantStatus int
		wantCode   string
		wantDetail string
	}{
		{
			name: "the version id is not in the tenant",
			arrange: func(d *w7cDeps) {
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(nil, domain.ErrNotFound)
			},
			wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantDetail: "resource not found",
		},
		{
			name: "the workflow row cannot be read after the version check passed",
			arrange: func(d *w7cDeps) {
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(w7cVersion(domain.VersionStatusPublished, "<x/>"), nil)
				d.workflows.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(nil, domain.ErrNotFound)
			},
			wantStatus: http.StatusNotFound, wantCode: "NOT_FOUND", wantDetail: "resource not found",
		},
		{
			name: "the version is a draft",
			arrange: func(d *w7cDeps) {
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(w7cVersion(domain.VersionStatusDraft, "<x/>"), nil)
			},
			wantStatus: http.StatusConflict, wantCode: "INVALID_VERSION_STATUS", wantDetail: "workflow version is not in PUBLISHED status",
		},
		{
			name: "the version is archived",
			arrange: func(d *w7cDeps) {
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(w7cVersion(domain.VersionStatusArchived, "<x/>"), nil)
			},
			wantStatus: http.StatusConflict, wantCode: "INVALID_VERSION_STATUS", wantDetail: "workflow version is not in PUBLISHED status",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, router := w7cRouter(t)
			tc.arrange(d)

			w := do(router, adminReq(http.MethodPost, w7cPromotePath(), nil))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			assert.Equal(t, "application/problem+json", strings.Split(w.Header().Get("Content-Type"), ";")[0])
			prob := decodeProblem(t, w)
			assert.Equal(t, tc.wantCode, prob["code"])
			assert.Contains(t, prob["detail"], tc.wantDetail)
			assert.EqualValues(t, tc.wantStatus, prob["status"])
			assert.Equal(t, w7cPromotePath(), prob["instance"])
		})
	}
}

func TestGetVersionValidation_ThroughTheRealServices_AnswersTheIssuesOfTheVersion(t *testing.T) {
	t.Parallel()
	mainCall := domain.ModuleCall{CallID: "CA_1", ProcessID: "Process_Review"}
	moduleID := uuid.New()
	published := domain.VersionStatusPublished
	one := int32(1)
	reviewV1 := &domain.ModuleVersion{
		ID: uuid.New(), ModuleID: moduleID, ProcessID: "Process_Review", VersionNumber: &one,
		Status: domain.VersionStatusPublished, BPMNXML: "<review/>",
	}
	cases := []struct {
		name       string
		version    *domain.WorkflowVersion
		arrange    func(d *w7cDeps)
		wantValid  bool
		wantIssues []bpmnIssueWant
	}{
		{
			name:    "a published version whose xml calls a module absent from its recorded modules",
			version: w7cVersion(published, "<main/>"),
			arrange: func(d *w7cDeps) {
				d.compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{mainCall}, nil, nil)
			},
			wantIssues: []bpmnIssueWant{{code: string(domain.BPMNErrUnresolvedCalledElement), nodeID: "CA_1", severity: string(domain.SeverityError)}},
		},
		{
			name: "a published version recording only another module",
			version: func() *domain.WorkflowVersion {
				v := w7cVersion(published, "<main/>")
				v.ModuleRefs = []domain.ModuleRef{{ModuleID: uuid.New(), VersionID: uuid.New(), ProcessID: "Process_Other", VersionNumber: 1}}
				return v
			}(),
			arrange: func(d *w7cDeps) {
				d.compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{mainCall}, nil, nil)
			},
			wantIssues: []bpmnIssueWant{{code: string(domain.BPMNErrUnresolvedCalledElement), nodeID: "CA_1", severity: string(domain.SeverityError)}},
		},
		{
			name:    "module issues are all that is reported and the compiler check is not run",
			version: w7cVersion(domain.VersionStatusDraft, "<main/>"),
			arrange: func(d *w7cDeps) {
				d.compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{mainCall}, nil, nil)
				d.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Review", int32(0)).Return(nil, domain.ErrNotFound)
				d.compiler.EXPECT().ValidateForPublish(gomock.Any(), gomock.Any()).Times(0)
				d.compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).Times(0)
			},
			wantIssues: []bpmnIssueWant{{code: string(domain.BPMNErrUnresolvedCalledElement), nodeID: "CA_1", severity: string(domain.SeverityError)}},
		},
		{
			name:    "a draft calling a published module has no issues",
			version: w7cVersion(domain.VersionStatusDraft, "<main/>"),
			arrange: func(d *w7cDeps) {
				d.compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{mainCall}, nil, nil)
				d.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Review", int32(0)).Return(reviewV1, nil)
				d.compiler.EXPECT().ModuleCalls(gomock.Any(), "<review/>").Return(nil, nil, nil)
				d.compiler.EXPECT().BundleModules("<main/>", []domain.ResolvedModule{{
					ProcessID: "Process_Review", VersionNumber: 0, PlanName: "Process_Review@v1", BPMNXML: "<review/>",
				}}).Return("<bundled/>", nil)
				d.compiler.EXPECT().ValidateForPublish(gomock.Any(), "<bundled/>").Return(nil, nil)
			},
			wantValid: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, router := w7cRouter(t)
			d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(tc.version, nil)
			tc.arrange(d)

			w := do(router, req(http.MethodGet, versionValidationPath(testWFID.String(), testVerID.String()), nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			body := decodeProblem(t, w)
			assert.Equal(t, tc.wantValid, body["is_valid"])
			assert.Equal(t, testVerID.String(), body["version_id"])
			assert.Equal(t, string(tc.version.Status), body["status"])
			if tc.wantValid {
				assert.Equal(t, []any{}, body["issues"], "no issues is an empty list, not null")
				return
			}
			assertBPMNIssues(t, body, tc.wantIssues)
		})
	}
}

func TestGetVersionValidation_TheDatabaseIsUnreachableWhileLoadingTheVersion_Returns503(t *testing.T) {
	t.Parallel()
	d, router := w7cRouter(t)
	outage := fmt.Errorf("query: %w: connection refused", domain.ErrUpstreamUnavailable)
	d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(nil, outage)

	w := do(router, req(http.MethodGet, versionValidationPath(testWFID.String(), testVerID.String()), nil))

	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	prob := decodeProblem(t, w)
	assert.Equal(t, "UPSTREAM_UNAVAILABLE", prob["code"])
	assert.Equal(t, "an upstream dependency is temporarily unavailable", prob["detail"])
	assert.NotContains(t, w.Body.String(), "connection refused")
	assert.NotContains(t, w.Body.String(), "is_valid", "an outage is not a validation verdict")
}

func TestGetVersionDiff_VersionsWhoseDocumentsCannotBeParsed_Return400InvalidBPMNXML(t *testing.T) {
	t.Parallel()
	unusable := "zzq not json"
	financePlan := diffFailureStoredPlan(t, "finance")
	cases := []struct {
		name         string
		base, target func(id uuid.UUID) *domain.WorkflowVersion
	}{
		{
			name: "a draft base",
			base: func(id uuid.UUID) *domain.WorkflowVersion {
				return diffFailureVersion(id, domain.VersionStatusDraft, w7cMalformedXML)
			},
			target: func(id uuid.UUID) *domain.WorkflowVersion {
				return diffFailureVersion(id, domain.VersionStatusDraft, diffFailureNoZeebeNamespace)
			},
		},
		{
			name: "a published base whose stored plan cannot be read",
			base: func(id uuid.UUID) *domain.WorkflowVersion {
				v := diffFailureVersion(id, domain.VersionStatusPublished, w7cMalformedXML)
				v.CompiledPlanJSON = &unusable
				return v
			},
			target: func(id uuid.UUID) *domain.WorkflowVersion {
				return diffFailureVersion(id, domain.VersionStatusDraft, diffFailureNoZeebeNamespace)
			},
		},
		{
			name: "a draft target",
			base: func(id uuid.UUID) *domain.WorkflowVersion {
				v := diffFailureVersion(id, domain.VersionStatusPublished, diffFailureBaseXML)
				v.CompiledPlanJSON = financePlan
				return v
			},
			target: func(id uuid.UUID) *domain.WorkflowVersion {
				return diffFailureVersion(id, domain.VersionStatusDraft, w7cMalformedXML)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, router := newDiffFailureRouter(t, true)
			baseID, targetID := uuid.New(), uuid.New()
			d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, baseID).Return(tc.base(baseID), nil)
			d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, targetID).Return(tc.target(targetID), nil)

			w := do(router, req(http.MethodGet, diffFailurePath(baseID, targetID), nil))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "INVALID_BPMN_XML", prob["code"])
			assert.Equal(t, "the BPMN document could not be parsed", prob["detail"])
			assert.NotContains(t, w.Body.String(), "invalid_params")
			assert.NotContains(t, w.Body.String(), "Process_Open", "the parser's text stays out of the response")
		})
	}
}

func TestGetVersionDiff_TwoVersionsWithTheSameCompiledPlan_Return200MetadataOnly(t *testing.T) {
	t.Parallel()
	equalCollab := &dsl.CompiledCollaboration{
		MainPlan:      "stored",
		Plans:         []*dsl.CompiledPlan{{Name: "stored", Departments: []dsl.DepartmentDef{{ID: "finance", Label: "finance"}, {ID: "operations", Label: "operations"}}}},
		SchemaVersion: dsl.CurrentSchemaVersion,
	}
	storedPlan := diffFailureStoredPlan(t, "finance", "operations")
	cases := []struct {
		name    string
		arrange func(d *diffFailureDeps, baseID, targetID uuid.UUID)
	}{
		{
			name: "both read from their stored plans",
			arrange: func(d *diffFailureDeps, baseID, targetID uuid.UUID) {
				base := diffFailureVersion(baseID, domain.VersionStatusPublished, diffFailureBaseXML)
				base.CompiledPlanJSON = storedPlan
				target := diffFailureVersion(targetID, domain.VersionStatusPublished, diffFailureTargetXML)
				target.CompiledPlanJSON = storedPlan
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, baseID).Return(base, nil)
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, targetID).Return(target, nil)
			},
		},
		{
			name: "the target is a draft compiled on the spot",
			arrange: func(d *diffFailureDeps, baseID, targetID uuid.UUID) {
				base := diffFailureVersion(baseID, domain.VersionStatusPublished, diffFailureBaseXML)
				base.CompiledPlanJSON = storedPlan
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, baseID).Return(base, nil)
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, targetID).
					Return(diffFailureVersion(targetID, domain.VersionStatusDraft, diffFailureTargetXML), nil)
				d.compiler.EXPECT().ModuleCalls(gomock.Any(), diffFailureTargetXML).Return(nil, nil, nil)
				d.compiler.EXPECT().BundleModules(diffFailureTargetXML, gomock.Any()).Return(diffFailureTargetXML, nil)
				d.compiler.EXPECT().CompileDraft(gomock.Any(), diffFailureTargetXML).Return(equalCollab, nil)
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

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			body := decodeProblem(t, w)
			assert.Equal(t, testWFID.String(), body["workflow_id"])
			assert.Equal(t, baseID.String(), body["base_version_id"])
			assert.Equal(t, targetID.String(), body["target_version_id"])
			assert.Equal(t, "METADATA_ONLY", body["change_type"])
			assert.Equal(t, map[string]any{
				"added_departments":   []any{},
				"removed_departments": []any{},
				"step_changes":        []any{},
				"stage_changes":       []any{},
				"metadata_only":       true,
			}, body["changes"])
		})
	}
}
