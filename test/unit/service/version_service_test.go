package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func buildPlan(deptIDs ...string) *dsl.CompiledPlan {
	depts := make([]dsl.DepartmentDef, 0, len(deptIDs))
	for _, id := range deptIDs {
		depts = append(depts, dsl.DepartmentDef{ID: id, Label: id})
	}
	return &dsl.CompiledPlan{Name: "test", Departments: depts}
}

func collabOf(plan *dsl.CompiledPlan) *dsl.CompiledCollaboration {
	return &dsl.CompiledCollaboration{
		MainPlan:      plan.Name,
		Plans:         []*dsl.CompiledPlan{plan},
		SchemaVersion: dsl.CurrentSchemaVersion,
	}
}

// marshalledPlan renders plan the way publish stores compiled_plan_json: as the
// main plan of a one-plan collaboration.
func marshalledPlan(t *testing.T, plan *dsl.CompiledPlan) *string {
	t.Helper()
	b, err := json.Marshal(collabOf(plan))
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	s := string(b)
	return &s
}

func TestVersionService_List_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID := uuid.New(), uuid.New()
	versions := []*domain.WorkflowVersion{{ID: uuid.New()}}
	vRepo.EXPECT().ListByWorkflow(gomock.Any(), tenantID, wfID, 1, 20).Return(versions, int64(1), nil)

	got, count, err := svc.List(context.Background(), tenantID, wfID, 1, 20)
	if err != nil || len(got) != 1 || count != 1 {
		t.Fatalf("unexpected: %v %v %v", got, count, err)
	}
}

func TestVersionService_List_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	vRepo.EXPECT().ListByWorkflow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, int64(0), errors.New("db error"))

	_, _, err := svc.List(context.Background(), uuid.New(), uuid.New(), 1, 10)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Get_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).
		Return(&domain.WorkflowVersion{ID: vID, WorkflowID: wfID}, nil)

	got, err := svc.Get(context.Background(), tenantID, wfID, vID)
	if err != nil || got == nil {
		t.Fatalf("unexpected: %v %v", got, err)
	}
}

func TestVersionService_Get_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	_, err := svc.Get(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Get_WorkflowMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).
		Return(&domain.WorkflowVersion{ID: vID, WorkflowID: uuid.New()}, nil)

	_, err := svc.Get(context.Background(), tenantID, wfID, vID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Publish_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Compiler: compiler,
	})

	tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>", RecordVersion: 7,
	}

	// Pre-flight (outside transaction)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(buildPlan()), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("abc123", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil) // divergence check: first publish
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil)

	// Transaction
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, int64(7), int32(1), gomock.Any(), "abc123", gomock.Any()).Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
	// No BulkInsert — empty plan produces no assignees
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)

	got, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, false)
	if err != nil || got == nil {
		t.Fatalf("unexpected: %v %v", got, err)
	}
	if got.Status != domain.VersionStatusPublished {
		t.Errorf("expected published status, got %s", got.Status)
	}
}

// Publishing a draft whose compiled plan structurally diverges from the active
// version must be rejected unless force is set.
func TestVersionService_Publish_CheckDivergence_WorkflowGetError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Workflows: wfRepo, Versions: vRepo, Compiler: compiler,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(buildPlan("a")), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(nil, errors.New("db error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error from workflow GetByID failure")
	}
}

func TestVersionService_Publish_CheckDivergence_ActiveVersionGetError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Workflows: wfRepo, Versions: vRepo, Compiler: compiler,
	})

	tenantID, wfID, vID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(buildPlan("a")), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{ActiveVersionID: &activeID}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeID).Return(nil, errors.New("db error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error from active version GetByID failure")
	}
}

func TestVersionService_Publish_StructuralDivergence(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Workflows: wfRepo, Versions: vRepo, Compiler: compiler,
	})

	tenantID, wfID, vID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(buildPlan("a")), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{ActiveVersionID: &activeID}, nil)
	// Active baseline has a different department set → structural divergence.
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeID).
		Return(&domain.WorkflowVersion{CompiledPlanJSON: marshalledPlan(t, buildPlan("b"))}, nil)

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if !errors.Is(err, domain.ErrStructuralDivergence) {
		t.Fatalf("expected ErrStructuralDivergence, got %v", err)
	}
}

// TestVersionService_Publish_UnreadableBaselineIsRecompiled pins that an
// active version whose stored plan cannot be read is recompiled for the
// divergence check, rather than letting any structural change through.
func TestVersionService_Publish_UnreadableBaselineIsRecompiled(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo, Compiler: compiler})

	tenantID, wfID, vID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	unreadable := "not valid json"
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(&domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusDraft, BPMNXML: "<draft/>",
	}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<draft/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<draft/>", gomock.Any()).Return("<draft/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<draft/>").Return(collabOf(buildPlan("a")), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<draft/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ActiveVersionID: &activeID}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeID).Return(&domain.WorkflowVersion{
		ID: activeID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusPublished,
		BPMNXML: "<active/>", CompiledPlanJSON: &unreadable,
	}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<active/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<active/>", gomock.Any()).Return("<active/>", nil)
	compiler.EXPECT().CompileDraft(gomock.Any(), "<active/>").Return(collabOf(buildPlan("b")), nil)

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if !errors.Is(err, domain.ErrStructuralDivergence) {
		t.Fatalf("err = %v, want ErrStructuralDivergence against the recompiled baseline", err)
	}
}

func TestVersionService_Publish_UnchangedStructureAgainstStoredBaseline_Publishes(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Compiler: compiler,
	})

	tenantID, userID, wfID, vID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(buildPlan("a")), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{ActiveVersionID: &activeID}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeID).
		Return(&domain.WorkflowVersion{CompiledPlanJSON: marshalledPlan(t, buildPlan("a"))}, nil)
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(2), nil)

	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, gomock.Any(), int32(2), gomock.Any(), "h", gomock.Any()).Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)

	if _, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, false); err != nil {
		t.Fatalf("same departments as the stored baseline must publish without force: %v", err)
	}
}

func TestVersionService_Publish_MainPlanMissingFromCollaboration_Fails(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}
	collab := collabOf(buildPlan("a"))
	collab.MainPlan = "absent"

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collab, nil)

	if _, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false); err == nil {
		t.Fatal("a collaboration whose main_plan names no plan must not publish")
	}
}

func planWithStage(stage dsl.StageDef) *dsl.CompiledPlan {
	return &dsl.CompiledPlan{
		Name:        "test",
		Departments: []dsl.DepartmentDef{{ID: "a", Label: "a", Stages: []dsl.StageDef{stage}}},
	}
}

func TestVersionService_Publish_StageBoundaryRewrite_IsStructural(t *testing.T) {
	plain := dsl.StageDef{Type: "task", NodeID: "T1", Role: "reviewer"}
	withTimer := plain
	withTimer.BoundaryTimer = &dsl.BoundaryTimer{BoundaryID: "B1", Duration: "P1D", Interrupting: true, Terminates: true}
	withMessage := plain
	withMessage.BoundaryMessage = &dsl.MessagePath{MessageName: "stop", BoundaryID: "B2", Interrupting: true, Terminates: true}
	otherConnector := plain
	otherConnector.ConnectorType = "send-email"

	for name, changed := range map[string]dsl.StageDef{"timer": withTimer, "message": withMessage, "connector type": otherConnector} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			compiler := mocks.NewMockPlanCompiler(ctrl)
			svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo, Compiler: compiler})

			tenantID, wfID, vID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(&domain.WorkflowVersion{
				ID: vID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
			}, nil)
			compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
			compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
			compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(planWithStage(changed)), nil)
			compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
			wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ActiveVersionID: &activeID}, nil)
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeID).
				Return(&domain.WorkflowVersion{CompiledPlanJSON: marshalledPlan(t, planWithStage(plain))}, nil)

			_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
			if !errors.Is(err, domain.ErrStructuralDivergence) {
				t.Fatalf("err = %v, want ErrStructuralDivergence: a %s that rewrites where the flow goes is a structural change", err, name)
			}
		})
	}
}

// force=true publishes through a structural divergence.
func TestVersionService_Publish_ForceStructural(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Compiler: compiler,
	})

	tenantID, userID, wfID, vID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(buildPlan("a")), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{ActiveVersionID: &activeID}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeID).
		Return(&domain.WorkflowVersion{CompiledPlanJSON: marshalledPlan(t, buildPlan("b"))}, nil)
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(2), nil)

	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, gomock.Any(), int32(2), gomock.Any(), "h", gomock.Any()).Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)

	got, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, true)
	if err != nil || got == nil {
		t.Fatalf("unexpected: %v %v", got, err)
	}
	if got.Status != domain.VersionStatusPublished {
		t.Errorf("expected published status, got %s", got.Status)
	}
}

// TestVersionService_Publish_BaselineThatCannotBeRead_StopsThePublish pins that
// a failure reading the active version's plan is returned as that failure,
// rather than skipping the divergence check.
func TestVersionService_Publish_BaselineThatCannotBeRead_StopsThePublish(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo, Compiler: compiler})

	tenantID, wfID, vID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(&domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusDraft, BPMNXML: "<draft/>",
	}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<draft/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<draft/>", gomock.Any()).Return("<draft/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<draft/>").Return(collabOf(buildPlan("a")), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<draft/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ActiveVersionID: &activeID}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeID).Return(&domain.WorkflowVersion{
		ID: activeID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusPublished, BPMNXML: "<active/>",
	}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<active/>").Return(nil, nil, errors.New("parse error"))

	if _, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false); err == nil || errors.Is(err, domain.ErrStructuralDivergence) {
		t.Fatalf("err = %v, want the failure reading the active version, not a divergence", err)
	}
}

// TestVersionService_Publish_BaselineThatNoLongerCompiles_IsDivergent pins that
// an active version whose stored plan is unreadable and whose BPMN no longer
// compiles under today's rules counts as divergent: refused without
// force_publish_structural, published with it, never blocked for good.
func TestVersionService_Publish_BaselineThatNoLongerCompiles_IsDivergent(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%v", force), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			tx := mocks.NewMockTransactor(ctrl)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			assignees := mocks.NewMockAssigneeRepository(ctrl)
			compiler := mocks.NewMockPlanCompiler(ctrl)
			svc := service.NewVersionService(service.VersionDeps{Transactor: tx, Workflows: wfRepo, Versions: vRepo, Assignees: assignees, Compiler: compiler})

			tenantID, wfID, vID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			unreadable := "not valid json"
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(&domain.WorkflowVersion{
				ID: vID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusDraft, BPMNXML: "<draft/>",
			}, nil)
			compiler.EXPECT().ModuleCalls(gomock.Any(), "<draft/>").Return(nil, nil, nil)
			compiler.EXPECT().BundleModules("<draft/>", gomock.Any()).Return("<draft/>", nil)
			compiler.EXPECT().CompileForPublish(gomock.Any(), "<draft/>").Return(collabOf(buildPlan("a")), nil)
			compiler.EXPECT().Hash(gomock.Any(), "<draft/>").Return("h", nil)
			wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ActiveVersionID: &activeID}, nil)
			vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeID).Return(&domain.WorkflowVersion{
				ID: activeID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusPublished,
				BPMNXML: "<active/>", CompiledPlanJSON: &unreadable,
			}, nil)
			compiler.EXPECT().ModuleCalls(gomock.Any(), "<active/>").Return(nil, nil, nil)
			compiler.EXPECT().BundleModules("<active/>", gomock.Any()).Return("<active/>", nil)
			compiler.EXPECT().CompileDraft(gomock.Any(), "<active/>").Return(nil, &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{
				{Code: domain.BPMNErrInvalidCandidateUser, NodeID: "T1", Severity: domain.SeverityError},
			}})
			if force {
				vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(2), nil)
				tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
					func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
				vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, gomock.Any(), int32(2), gomock.Any(), "h", gomock.Any()).Return(nil)
				assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
				wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)
			}

			_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, force)
			if force && err != nil {
				t.Errorf("forced publish: %v", err)
			}
			if !force && !errors.Is(err, domain.ErrStructuralDivergence) {
				t.Errorf("err = %v, want ErrStructuralDivergence", err)
			}
		})
	}
}

func TestVersionService_Publish_GetVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(nil, errors.New("db failure"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error from GetByID failure")
	}
}

func TestVersionService_Publish_VersionNotDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if !errors.Is(err, domain.ErrVersionNotDraft) {
		t.Fatalf("expected ErrVersionNotDraft, got %v", err)
	}
}

func TestVersionService_Publish_WorkflowMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: uuid.New(), Status: domain.VersionStatusDraft}, nil)

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Publish_BundleError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bad/>"}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bad/>").Return(nil, nil, nil)

	compiler.EXPECT().BundleModules("<bad/>", gomock.Any()).Return("", errors.New("malformed module BPMN"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_CompileError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bad/>"}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bad/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bad/>", gomock.Any()).Return("<bad/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bad/>").Return(nil, errors.New("parse error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_EligibilityFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	membership := mocks.NewMockMembershipService(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Versions: vRepo, Compiler: compiler, Membership: membership,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	assigneeID := uuid.Must(uuid.NewV7())
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{{
			ID:              "finance",
			IAMDepartmentID: "finance",
			Stages: []dsl.StageDef{{
				Type:             "review",
				Role:             "reviewer",
				DefaultAssignees: []string{assigneeID.String()},
			}},
		}},
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(plan), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("hash", nil)
	membership.EXPECT().CheckEligibility(gomock.Any(), tenantID, assigneeID, "finance", "reviewer").
		Return(false, nil)

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if !errors.Is(err, domain.ErrAssigneeIneligible) {
		t.Fatalf("expected ErrAssigneeIneligible, got %v", err)
	}
}

func TestVersionService_Publish_SkipEligibilityCheck(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	membership := mocks.NewMockMembershipService(ctrl) // set but must not be called
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Compiler: compiler, Membership: membership,
	})

	tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(buildPlan()), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil) // divergence check: first publish
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(2), nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	// forcePublishStructural=true — structural divergence bypassed; eligibility still runs (empty plan, no assignees)
	got, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, true)
	if err != nil || got == nil {
		t.Fatalf("unexpected: %v %v", got, err)
	}
}

func TestVersionService_Publish_WithAssignees_BulkInsert(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Compiler: compiler,
	})

	tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	assigneeID := uuid.Must(uuid.NewV7())
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{{
			ID:              "ops",
			IAMDepartmentID: uuid.New().String(),
			Stages: []dsl.StageDef{{
				Type:             "approve",
				Role:             "manager",
				DefaultAssignees: []string{assigneeID.String()},
			}},
		}},
	}
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(plan), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("hash", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil) // divergence check: first publish
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, gomock.Any(), int32(1), gomock.Any(), "hash", gomock.Any()).Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
	assignees.EXPECT().BulkInsert(gomock.Any(), tenantID, vID, gomock.Any()).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)

	got, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, false)
	if err != nil || got == nil {
		t.Fatalf("unexpected: %v %v", got, err)
	}
}

func TestVersionService_Clone_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{PlanLimits: stdPlanLimits(),
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
	})

	tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	source := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, BPMNXML: "<bpmn/>",
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(source, nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	newWF, newV, err := svc.Clone(context.Background(), tenantID, userID, wfID, vID, "enterprise",
		service.CloneReq{NewKey: "new-key", NewName: "New", NewDescription: "Copy"})
	if err != nil || newWF == nil || newV == nil {
		t.Fatalf("unexpected: %v %v %v", newWF, newV, err)
	}
	if newV.BPMNXML != "<bpmn/>" {
		t.Errorf("expected BPMN copied from source")
	}
}

func TestVersionService_Clone_SourceIsDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft}, nil)

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise", service.CloneReq{})
	if !errors.Is(err, domain.ErrVersionNotPublished) {
		t.Fatalf("expected ErrVersionNotPublished, got %v", err)
	}
}

func TestVersionService_Clone_WorkflowMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: uuid.New(), Status: domain.VersionStatusPublished}, nil)

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise", service.CloneReq{})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Clone_GetSourceError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	_, _, err := svc.Clone(context.Background(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), "enterprise", service.CloneReq{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Promote_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	prevID := uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, IsValid: true}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(
		&domain.Workflow{ID: wfID, ActiveVersionID: &prevID}, nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)

	if _, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVersionService_Promote_AlreadyActive(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, IsValid: true}, nil)
	// Target is already active → early exit: no transactor, no UpdateActiveVersion.
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(
		&domain.Workflow{ID: wfID, ActiveVersionID: &vID}, nil)

	if _, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVersionService_Promote_NotPublished(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft}, nil)

	_, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise")
	if !errors.Is(err, domain.ErrVersionNotPublished) {
		t.Fatalf("expected ErrVersionNotPublished, got %v", err)
	}
}

func TestVersionService_Promote_VersionMarkedInvalid_IsRefused(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, IsValid: false}, nil)

	_, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise")
	if !errors.Is(err, domain.ErrInvalidVersionStatus) {
		t.Fatalf("expected ErrInvalidVersionStatus for a version whose assignees are revoked, got %v", err)
	}
}

func TestVersionService_Promote_WorkflowMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: uuid.New(), Status: domain.VersionStatusPublished, IsValid: true}, nil)

	_, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Promote_WorkflowGetError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, IsValid: true}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(nil, errors.New("db error"))

	if _, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise"); err == nil {
		t.Fatal("expected error from workflow GetByID failure")
	}
}

func TestVersionService_Promote_GetVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	if _, err := svc.Promote(context.Background(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), "enterprise"); err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Promote_UpdateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo, PlanLimits: stdPlanLimits(),
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, IsValid: true}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(
		&domain.Workflow{ID: wfID}, nil)
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(&domain.WorkflowVersion{}, nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(errors.New("db error"))

	if _, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise"); err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Promote_WithVersionNumberAndPlan(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo, PlanLimits: stdPlanLimits(),
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vNum := int32(5)
	plan := `{"name":"p"}`
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(&domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, IsValid: true,
		VersionNumber: &vNum, CompiledPlanJSON: &plan,
	}, nil)
	// No active version on the target workflow.
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(
		&domain.Workflow{ID: wfID}, nil)
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(&domain.WorkflowVersion{}, nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)

	if _, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func promoteReactivationFixture(t *testing.T) (*service.VersionService, *mocks.MockTransactor, *mocks.MockWorkflowRepository, *mocks.MockWorkflowVersionRepository, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo, PlanLimits: stdPlanLimits(),
	})
	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished, IsValid: true}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ID: wfID}, nil)
	return svc, tx, wfRepo, vRepo, tenantID, wfID, vID
}

func runTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

func TestVersionService_Promote_ReactivationAtTheLimitIsRefused(t *testing.T) {
	svc, tx, wfRepo, vRepo, tenantID, wfID, vID := promoteReactivationFixture(t)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(runTx)
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(nil, domain.ErrNotFound)
	wfRepo.EXPECT().CountByTenant(gomock.Any(), tenantID).Return(int64(5), nil)

	_, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "starter")
	if !errors.Is(err, domain.ErrPlanQuotaExceeded) {
		t.Fatalf("err = %v, want ErrPlanQuotaExceeded", err)
	}
}

func TestVersionService_Promote_ReactivationUnderTheLimitSucceeds(t *testing.T) {
	svc, tx, wfRepo, vRepo, tenantID, wfID, vID := promoteReactivationFixture(t)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(runTx)
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(nil, domain.ErrNotFound)
	wfRepo.EXPECT().CountByTenant(gomock.Any(), tenantID).Return(int64(4), nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)

	if _, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "starter"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVersionService_Promote_ReactivationOfAWorkflowWithADraftIsNotCountedAgain(t *testing.T) {
	svc, tx, wfRepo, vRepo, tenantID, wfID, vID := promoteReactivationFixture(t)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(runTx)
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(&domain.WorkflowVersion{}, nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)

	if _, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "starter"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVersionService_Promote_ReactivationWithAnUnknownPlanIsUnavailable(t *testing.T) {
	svc, _, _, _, tenantID, wfID, vID := promoteReactivationFixture(t)

	_, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "platinum")
	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Fatalf("err = %v, want ErrUpstreamUnavailable", err)
	}
}

func TestVersionService_Promote_ReactivationDraftLookupFailureIsReturned(t *testing.T) {
	svc, tx, _, vRepo, tenantID, wfID, vID := promoteReactivationFixture(t)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(runTx)
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(nil, errors.New("db down"))

	if _, err := svc.Promote(context.Background(), tenantID, uuid.New(), wfID, vID, "starter"); err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Export_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vNum := int32(3)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, BPMNXML: "<bpmn/>", VersionNumber: &vNum}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(
		&domain.Workflow{TemplateKey: "tender-review"}, nil)

	xml, filename, err := svc.Export(context.Background(), tenantID, wfID, vID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if xml != "<bpmn/>" {
		t.Errorf("unexpected xml: %s", xml)
	}
	if filename != "tender-review-v3.bpmn" {
		t.Errorf("unexpected filename: %s", filename)
	}
}

func TestVersionService_Export_Draft_Filename(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, BPMNXML: "<bpmn/>", VersionNumber: nil}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(
		&domain.Workflow{TemplateKey: "my-wf"}, nil)

	_, filename, err := svc.Export(context.Background(), tenantID, wfID, vID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filename != "my-wf-vdraft.bpmn" {
		t.Errorf("unexpected filename: %s", filename)
	}
}

func TestVersionService_Export_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.ErrNotFound)

	_, _, err := svc.Export(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Export_WorkflowMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(
		&domain.WorkflowVersion{WorkflowID: uuid.New()}, nil)

	_, _, err := svc.Export(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Export_WorkflowGetError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Workflows: wfRepo, Versions: vRepo})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID}, nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(nil, errors.New("db error"))

	_, _, err := svc.Export(context.Background(), tenantID, wfID, vID)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Diff_UsesStoredPlan(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID := uuid.New(), uuid.New()
	baseID, targetID := uuid.New(), uuid.New()

	basePlan := buildPlan("finance")
	targetPlan := buildPlan("finance", "operations")

	// compiler.Compile must NOT be called — stored JSON used
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, CompiledPlanJSON: marshalledPlan(t, basePlan)}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, CompiledPlanJSON: marshalledPlan(t, targetPlan)}, nil)

	result, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ChangeType != "STRUCTURAL" {
		t.Errorf("expected STRUCTURAL, got %s", result.ChangeType)
	}
	if len(result.Changes.AddedDepartments) != 1 || result.Changes.AddedDepartments[0] != "operations" {
		t.Errorf("expected operations added, got %v", result.Changes.AddedDepartments)
	}
}

func TestVersionService_Diff_FallsBackToCompile(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID := uuid.New(), uuid.New()
	baseID, targetID := uuid.New(), uuid.New()

	// No stored compiled plan — compiler.Compile must be called for both
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, BPMNXML: "<base/>"}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, BPMNXML: "<target/>"}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<base/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<base/>", gomock.Any()).Return("<base/>", nil)
	compiler.EXPECT().CompileDraft(gomock.Any(), "<base/>").Return(collabOf(buildPlan("a")), nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<target/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<target/>", gomock.Any()).Return("<target/>", nil)
	compiler.EXPECT().CompileDraft(gomock.Any(), "<target/>").Return(collabOf(buildPlan("a")), nil)

	result, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ChangeType != "METADATA_ONLY" {
		t.Errorf("expected METADATA_ONLY, got %s", result.ChangeType)
	}
}

// TestVersionService_Diff_UnmarshalFailureFallsBackToCompile covers resolvePlan's
// other fallback trigger: CompiledPlanJSON is present (non-empty) but fails to
// unmarshal, as opposed to TestVersionService_Diff_FallsBackToCompile's case of
// CompiledPlanJSON being entirely absent. Both must fall through to
// library resolution (compiler.ModuleCalls + compiler.BundleModules) and
// compiler.Compile.
func TestVersionService_Diff_UnmarshalFailureFallsBackToCompile(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID := uuid.New(), uuid.New()
	baseID, targetID := uuid.New(), uuid.New()
	garbage := "not valid json"

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, BPMNXML: "<base/>", CompiledPlanJSON: &garbage}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, BPMNXML: "<target/>", CompiledPlanJSON: &garbage}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<base/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<base/>", gomock.Any()).Return("<base/>", nil)
	compiler.EXPECT().CompileDraft(gomock.Any(), "<base/>").Return(collabOf(buildPlan("a")), nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<target/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<target/>", gomock.Any()).Return("<target/>", nil)
	compiler.EXPECT().CompileDraft(gomock.Any(), "<target/>").Return(collabOf(buildPlan("a")), nil)

	result, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ChangeType != "METADATA_ONLY" {
		t.Errorf("expected METADATA_ONLY, got %s", result.ChangeType)
	}
}

func TestVersionService_Diff_WorkflowMismatch_Base(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID := uuid.New(), uuid.New()
	baseID, targetID := uuid.New(), uuid.New()

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: uuid.New()}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID}, nil)

	_, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVersionService_Diff_GetBaseError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, baseID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(nil, domain.ErrNotFound)

	_, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Diff_GetTargetError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})

	tenantID, wfID, baseID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(nil, domain.ErrNotFound)

	_, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Diff_CompileBaseError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, baseID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, BPMNXML: "<base/>"}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, BPMNXML: "<target/>"}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<base/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<base/>", gomock.Any()).Return("<base/>", nil)
	compiler.EXPECT().CompileDraft(gomock.Any(), "<base/>").Return(nil, errors.New("compile error"))

	_, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Diff_CompileTargetError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, baseID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, BPMNXML: "<base/>"}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, BPMNXML: "<target/>"}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<base/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<base/>", gomock.Any()).Return("<base/>", nil)
	compiler.EXPECT().CompileDraft(gomock.Any(), "<base/>").Return(collabOf(buildPlan()), nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<target/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<target/>", gomock.Any()).Return("<target/>", nil)
	compiler.EXPECT().CompileDraft(gomock.Any(), "<target/>").Return(nil, errors.New("compile error"))

	_, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Diff_WithStepChanges(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, baseID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	basePlan := &dsl.CompiledPlan{
		Execution: dsl.ExecutionPlan{
			Steps: []dsl.ExecutionStep{{Sequential: []string{"step-a"}}},
		},
	}
	targetPlan := &dsl.CompiledPlan{
		Execution: dsl.ExecutionPlan{
			Steps: []dsl.ExecutionStep{
				{Sequential: []string{"step-a"}},
				{Sequential: []string{"step-b"}},
			},
		},
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, BPMNXML: "<base/>"}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, BPMNXML: "<target/>"}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<base/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<base/>", gomock.Any()).Return("<base/>", nil)
	compiler.EXPECT().CompileDraft(gomock.Any(), "<base/>").Return(collabOf(basePlan), nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<target/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<target/>", gomock.Any()).Return("<target/>", nil)
	compiler.EXPECT().CompileDraft(gomock.Any(), "<target/>").Return(collabOf(targetPlan), nil)

	result, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ChangeType != "STRUCTURAL" {
		t.Errorf("expected STRUCTURAL (step added), got %s", result.ChangeType)
	}
	if len(result.Changes.StepChanges) != 1 {
		t.Errorf("expected 1 step change, got %d", len(result.Changes.StepChanges))
	}
}

func TestVersionService_Diff_StepRemoved(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, baseID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	// Base has 2 steps, target has 1 — step removed
	basePlan := &dsl.CompiledPlan{
		Execution: dsl.ExecutionPlan{
			Steps: []dsl.ExecutionStep{
				{Sequential: []string{"a"}},
				{Sequential: []string{"b"}},
			},
		},
	}
	targetPlan := &dsl.CompiledPlan{
		Execution: dsl.ExecutionPlan{
			Steps: []dsl.ExecutionStep{{Sequential: []string{"a"}}},
		},
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, BPMNXML: "<b/>"}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, BPMNXML: "<t/>"}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<b/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<b/>", gomock.Any()).Return("<b/>", nil)
	compiler.EXPECT().CompileDraft(gomock.Any(), "<b/>").Return(collabOf(basePlan), nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<t/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<t/>", gomock.Any()).Return("<t/>", nil)
	compiler.EXPECT().CompileDraft(gomock.Any(), "<t/>").Return(collabOf(targetPlan), nil)

	result, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Changes.StepChanges) != 1 {
		t.Errorf("expected 1 step change (removal), got %d", len(result.Changes.StepChanges))
	}
}

func publishPreFlightMocks(
	t *testing.T,
	vRepo *mocks.MockWorkflowVersionRepository,
	compiler *mocks.MockPlanCompiler,
	tenantID, wfID, vID uuid.UUID,
	plan *dsl.CompiledPlan,
) {
	t.Helper()
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(plan), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil)
}

func TestVersionService_Publish_HashError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(buildPlan()), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("", errors.New("hash error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_NextVersionNumberError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Transactor: tx, Versions: vRepo, Compiler: compiler})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(buildPlan()), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(0), errors.New("db error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_DeleteByVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Versions: vRepo, Assignees: assignees, Compiler: compiler,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	publishPreFlightMocks(t, vRepo, compiler, tenantID, wfID, vID, buildPlan())
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("delete error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_BulkInsertError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Versions: vRepo, Assignees: assignees, Compiler: compiler,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	assigneeID := uuid.Must(uuid.NewV7())
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{{
			ID:              "hr",
			IAMDepartmentID: uuid.New().String(),
			Stages: []dsl.StageDef{{
				Type:             "approve",
				Role:             "manager",
				DefaultAssignees: []string{assigneeID.String()},
			}},
		}},
	}
	publishPreFlightMocks(t, vRepo, compiler, tenantID, wfID, vID, plan)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	assignees.EXPECT().BulkInsert(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("bulk error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_UpdateActiveVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo, Assignees: assignees, Compiler: compiler,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	publishPreFlightMocks(t, vRepo, compiler, tenantID, wfID, vID, buildPlan())
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil) // divergence check: first publish
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("update error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_InvalidAssigneeUUID(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	membership := mocks.NewMockMembershipService(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Versions: vRepo, Compiler: compiler, Membership: membership,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{{
			ID: "legal",
			Stages: []dsl.StageDef{{
				Type:             "review",
				Role:             "approver",
				DefaultAssignees: []string{"not-a-valid-uuid"},
			}},
		}},
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(plan), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if !errors.Is(err, domain.ErrAssigneeIneligible) {
		t.Fatalf("expected ErrAssigneeIneligible for invalid UUID, got %v", err)
	}
}

func TestVersionService_Publish_EligibilityCheckError(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	membership := mocks.NewMockMembershipService(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Versions: vRepo, Compiler: compiler, Membership: membership,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	assigneeID := uuid.Must(uuid.NewV7())
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{{
			ID:              "ops",
			IAMDepartmentID: "ops",
			Stages: []dsl.StageDef{{
				Type:             "approve",
				Role:             "manager",
				DefaultAssignees: []string{assigneeID.String()},
			}},
		}},
	}
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(plan), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	membership.EXPECT().CheckEligibility(gomock.Any(), tenantID, assigneeID, "ops", "manager").
		Return(false, errors.New("membership service error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Clone_CreateWorkflowError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{PlanLimits: stdPlanLimits(),
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusPublished, BPMNXML: "<bpmn/>"}, nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("duplicate key"))

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise", service.CloneReq{NewKey: "copy"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Clone_CreateVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{PlanLimits: stdPlanLimits(),
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusPublished, BPMNXML: "<bpmn/>"}, nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("version error"))

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "enterprise", service.CloneReq{NewKey: "copy"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_PublishVersionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Versions: vRepo, Assignees: assignees, Compiler: compiler,
	})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	publishPreFlightMocks(t, vRepo, compiler, tenantID, wfID, vID, buildPlan())
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("publish db error"))

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Publish_EligibleAssignees_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	membership := mocks.NewMockMembershipService(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Compiler: compiler, Membership: membership,
	})

	tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	assigneeID := uuid.Must(uuid.NewV7())
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{{
			ID:              "finance",
			IAMDepartmentID: "018e1f2a-0000-7000-8000-000000000030",
			Stages: []dsl.StageDef{{
				Type:             "approve",
				Role:             "manager",
				DefaultAssignees: []string{assigneeID.String()},
			}},
		}},
	}
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(plan), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil) // divergence check: first publish
	// All assignees eligible → checkAssigneeEligibility returns nil
	membership.EXPECT().CheckEligibility(gomock.Any(), tenantID, assigneeID, "018e1f2a-0000-7000-8000-000000000030", "manager").
		Return(true, nil)
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, gomock.Any(), int32(1), gomock.Any(), "h", gomock.Any()).Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
	assignees.EXPECT().BulkInsert(gomock.Any(), tenantID, vID, gomock.Any()).Return(nil)
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)

	got, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, false)
	if err != nil || got == nil {
		t.Fatalf("unexpected: %v %v", got, err)
	}
}

func TestVersionService_Publish_ExtractAssignees_InvalidUUID_Skipped(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Compiler: compiler,
	})

	tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{{
			ID:              "ops",
			IAMDepartmentID: uuid.New().String(),
			Stages: []dsl.StageDef{{
				Type:             "review",
				Role:             "reviewer",
				DefaultAssignees: []string{"not-a-uuid"}, // invalid UUID skipped in extractAssignees
			}},
		}},
	}
	draft := &domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
	}

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(draft, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(plan), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("h", nil)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil) // divergence check: first publish
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, gomock.Any(), int32(1), gomock.Any(), "h", gomock.Any()).Return(nil)
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil)
	// BulkInsert NOT called — invalid UUID skipped by extractAssignees → empty slice
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil)

	// forcePublishStructural=true; no membership dep → eligibility skipped
	got, err := svc.Publish(context.Background(), tenantID, userID, wfID, vID, true)
	if err != nil || got == nil {
		t.Fatalf("unexpected: %v %v", got, err)
	}
}

func TestVersionService_Diff_DepartmentRemoved(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler})

	tenantID, wfID, baseID, targetID := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	// Base has 2 depts, target has 1 — "old-dept" is removed
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(
		&domain.WorkflowVersion{ID: baseID, WorkflowID: wfID, BPMNXML: "<b/>"}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(
		&domain.WorkflowVersion{ID: targetID, WorkflowID: wfID, BPMNXML: "<t/>"}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<b/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<b/>", gomock.Any()).Return("<b/>", nil)
	compiler.EXPECT().CompileDraft(gomock.Any(), "<b/>").Return(collabOf(buildPlan("finance", "old-dept")), nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<t/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<t/>", gomock.Any()).Return("<t/>", nil)
	compiler.EXPECT().CompileDraft(gomock.Any(), "<t/>").Return(collabOf(buildPlan("finance")), nil)

	result, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ChangeType != "STRUCTURAL" {
		t.Errorf("expected STRUCTURAL, got %s", result.ChangeType)
	}
	if len(result.Changes.RemovedDepartments) != 1 || result.Changes.RemovedDepartments[0] != "old-dept" {
		t.Errorf("expected old-dept removed, got %v", result.Changes.RemovedDepartments)
	}
}

func TestVersionService_Clone_QuotaNotExceeded(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := txPassthrough(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()

	svc := service.NewVersionService(service.VersionDeps{PlanLimits: stdPlanLimits(),
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
	})

	wfRepo.EXPECT().CountByTenant(gomock.Any(), tenantID).Return(int64(3), nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)
	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	newWF, newV, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "starter",
		service.CloneReq{NewKey: "k", NewName: "n"})
	if err != nil || newWF == nil || newV == nil {
		t.Fatalf("unexpected: err=%v wf=%v v=%v", err, newWF, newV)
	}
}

func TestVersionService_Clone_QuotaExceeded(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := txPassthrough(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()

	svc := service.NewVersionService(service.VersionDeps{PlanLimits: stdPlanLimits(), Transactor: tx, Workflows: wfRepo, Versions: vRepo})

	// Source is fetched before the (serializable) tx; the quota COUNT now runs inside it.
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)
	wfRepo.EXPECT().CountByTenant(gomock.Any(), tenantID).Return(int64(5), nil)

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "starter",
		service.CloneReq{NewKey: "k", NewName: "n"})
	if !errors.Is(err, domain.ErrPlanQuotaExceeded) {
		t.Fatalf("expected ErrPlanQuotaExceeded, got %v", err)
	}
}

func TestVersionService_Clone_QuotaCountError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := txPassthrough(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()

	svc := service.NewVersionService(service.VersionDeps{PlanLimits: stdPlanLimits(), Transactor: tx, Workflows: wfRepo, Versions: vRepo})

	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)
	wfRepo.EXPECT().CountByTenant(gomock.Any(), tenantID).Return(int64(0), errors.New("db error"))

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "pro",
		service.CloneReq{NewKey: "k", NewName: "n"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVersionService_Clone_NoLimitSourceIsUnavailableNeverAFreeTier(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	svc := service.NewVersionService(service.VersionDeps{Transactor: txPassthrough(ctrl), Workflows: mocks.NewMockWorkflowRepository(ctrl), Versions: vRepo})
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "starter", service.CloneReq{NewKey: "k", NewName: "n"})
	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Fatalf("expected ErrUpstreamUnavailable, got %v", err)
	}
}

func TestVersionService_Clone_UnknownPlanCodeIsUnavailable(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	svc := service.NewVersionService(service.VersionDeps{
		PlanLimits: stdPlanLimits(), Transactor: txPassthrough(ctrl), Workflows: mocks.NewMockWorkflowRepository(ctrl), Versions: vRepo,
	})
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{ID: vID, WorkflowID: wfID, Status: domain.VersionStatusPublished}, nil)

	_, _, err := svc.Clone(context.Background(), tenantID, uuid.New(), wfID, vID, "", service.CloneReq{NewKey: "k", NewName: "n"})
	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Fatalf("expected ErrUpstreamUnavailable, got %v", err)
	}
}
