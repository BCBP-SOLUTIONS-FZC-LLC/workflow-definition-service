package service_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
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

// publishWithRealCompiler publishes bpmnXML through the real compiler and
// returns the compiled_plan_json handed to the repository.
func publishWithRealCompiler(t *testing.T, bpmnXML string) (string, error) {
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
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil).AnyTimes()
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil).AnyTimes()
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil).AnyTimes()
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }).AnyTimes()
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, int32(1), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ uuid.UUID, _ int32, compiledPlanJSON, _ string) error {
			stored = compiledPlanJSON
			return nil
		}).AnyTimes()
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil).AnyTimes()
	assignees.EXPECT().BulkInsert(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil).AnyTimes()

	_, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, false)
	var vf *domain.ValidationFailedError
	if errors.As(err, &vf) {
		t.Logf("validation errors: %+v", vf.Errors)
	}
	return stored, err
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
