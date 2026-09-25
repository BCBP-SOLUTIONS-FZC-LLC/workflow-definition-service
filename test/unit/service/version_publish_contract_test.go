package service_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

//go:embed testdata/single-process.bpmn
var singleProcessBPMN string

//go:embed testdata/call-activity.bpmn
var callActivityBPMN string

//go:embed testdata/bnb.bpmn
var bnbCollaborationBPMN string

//go:embed testdata/module-main.bpmn
var moduleMainBPMN string

//go:embed testdata/module-prep-review.bpmn
var prepReviewModuleBPMN string

//go:embed testdata/module-prep-review-invalid-candidate-user.bpmn
var prepReviewModuleInvalidCandidateUserBPMN string

//go:embed testdata/module-self-calling.bpmn
var selfCallingModuleBPMN string

//go:embed testdata/two-calls-of-one-module.bpmn
var twoCallsOfOneModuleBPMN string

// publishWithRealCompiler publishes bpmnXML through the real compiler and
// returns the compiled_plan_json handed to the repository.
func publishWithRealCompiler(t *testing.T, bpmnXML string) (string, error) {
	t.Helper()
	stored, _, err := publishRecordingAssignees(t, bpmnXML)
	return stored, err
}

// publishRecordingAssignees is publishWithRealCompiler that also returns the
// workflow_node_assignee rows handed to the repository.
func publishRecordingAssignees(t *testing.T, bpmnXML string) (string, []*domain.NodeAssignee, error) {
	t.Helper()
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Compiler: bpmn_compiler.New(),
	})

	tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: bpmnXML,
	}

	var stored string
	var rows []*domain.NodeAssignee
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil).AnyTimes()
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil).AnyTimes()
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil).AnyTimes()
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }).AnyTimes()
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, int32(1), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ uuid.UUID, _ int32, compiledPlanJSON, _ string, _ []domain.ModuleRef) error {
			stored = compiledPlanJSON
			return nil
		}).AnyTimes()
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil).AnyTimes()
	assignees.EXPECT().BulkInsert(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ uuid.UUID, inserted []*domain.NodeAssignee) error {
			rows = inserted
			return nil
		}).AnyTimes()
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil).AnyTimes()

	_, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, false)
	var vf *domain.ValidationFailedError
	if errors.As(err, &vf) {
		t.Logf("validation errors: %+v", vf.Errors)
	}
	return stored, rows, err
}

// assertExecutionCanStart reads compiled_plan_json the way the Execution
// Service does (dsl.CompiledCollaboration, main plan looked up by name).
func assertExecutionCanStart(t *testing.T, compiledPlanJSON string) *dsl.CompiledCollaboration {
	t.Helper()
	var collab dsl.CompiledCollaboration
	if err := json.Unmarshal([]byte(compiledPlanJSON), &collab); err != nil {
		t.Fatalf("compiled_plan_json does not decode as CompiledCollaboration: %v", err)
	}
	if collab.SchemaVersion != dsl.CurrentSchemaVersion {
		t.Errorf("schema_version = %d, want %d", collab.SchemaVersion, dsl.CurrentSchemaVersion)
	}
	if collab.MainPlan == "" {
		t.Fatalf("main_plan is empty; Execution fails with \"main plan not found\"")
	}
	if _, err := dsl.ExpandCalls(&collab, 0); err != nil {
		t.Fatalf("compiled_plan_json does not expand as Execution expands it: %v", err)
	}
	for _, p := range collab.Plans {
		if p.Name == collab.MainPlan {
			return &collab
		}
	}
	t.Fatalf("main_plan %q is not among the %d compiled plans", collab.MainPlan, len(collab.Plans))
	return nil
}

func TestPublishContract_SingleProcess_StoresStartableCollaboration(t *testing.T) {
	stored, err := publishWithRealCompiler(t, singleProcessBPMN)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	collab := assertExecutionCanStart(t, stored)
	if len(collab.Plans) != 1 {
		t.Errorf("plans = %d, want 1", len(collab.Plans))
	}
}

func TestPublishContract_UnnamedProcess_StoresStartableCollaboration(t *testing.T) {
	unnamed := strings.Replace(singleProcessBPMN, ` name="Engineering Package Review"`, "", 1)
	if unnamed == singleProcessBPMN {
		t.Fatal("fixture no longer carries the process name this test strips")
	}
	stored, err := publishWithRealCompiler(t, unnamed)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	assertExecutionCanStart(t, stored)
}

func TestPublishContract_CallActivity_Publishes(t *testing.T) {
	stored, err := publishWithRealCompiler(t, callActivityBPMN)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	assertExecutionCanStart(t, stored)
}

func TestPublishContract_CollaborationWithIgnoredPool_Publishes(t *testing.T) {
	stored, err := publishWithRealCompiler(t, bnbCollaborationBPMN)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	assertExecutionCanStart(t, stored)
}

func prepReviewLibrary(bpmnXML string) map[libraryKey]*domain.ModuleVersion {
	return map[libraryKey]*domain.ModuleVersion{
		{"Process_PrepReview", 0}: libraryVersion(uuid.New(), "Process_PrepReview", 1, bpmnXML),
	}
}

func TestPublishContract_MainWithModule_Publishes(t *testing.T) {
	got := publishWithLibrary(t, moduleMainBPMN, prepReviewLibrary(prepReviewModuleBPMN))
	if got.err != nil {
		t.Fatalf("publish: %v", got.err)
	}
	assertExecutionCanStart(t, got.stored)
}

func TestPublishContract_ModuleWithInvalidTask_IsRefused(t *testing.T) {
	got := publishWithLibrary(t, moduleMainBPMN, prepReviewLibrary(prepReviewModuleInvalidCandidateUserBPMN))
	assertRefusedWith(t, got.stored, got.err, domain.BPMNErrInvalidCandidateUser)
}

func TestPublishContract_SelfCallingModule_IsRefused(t *testing.T) {
	got := publishWithLibrary(t, moduleMainBPMN, prepReviewLibrary(selfCallingModuleBPMN))
	assertRefusedWith(t, got.stored, got.err, domain.BPMNErrCalledElementCycle)
}

func assertRefusedWith(t *testing.T, stored string, err error, code domain.BPMNErrorCode) {
	t.Helper()
	var vf *domain.ValidationFailedError
	if !errors.As(err, &vf) {
		t.Fatalf("publish err = %v, want a ValidationFailedError carrying %s", err, code)
	}
	found := false
	for _, e := range vf.Errors {
		found = found || e.Code == code
	}
	if !found {
		t.Errorf("publish errors = %+v, want %s", vf.Errors, code)
	}
	if stored != "" {
		t.Errorf("a refused publish stored compiled_plan_json")
	}
}

func TestPublishContract_TwoCallsOfOneModule_RecordAnAssigneePerCall(t *testing.T) {
	_, rows, err := publishRecordingAssignees(t, twoCallsOfOneModuleBPMN)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.NodeKey] = r.DepartmentID.String()
	}
	want := map[string]string{
		"CA_1::Process_Review/Review_Task": "2200a5ae-2466-54f7-a1d8-243106e05705",
		"CA_2::Process_Review/Review_Task": "92fd92cf-4b2c-58dc-bf98-f3d0af729876",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("assignee rows (node key → department) = %v, want %v", got, want)
	}
}
