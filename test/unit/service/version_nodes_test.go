package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl/dsltest"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func nodesService(t *testing.T, v *domain.WorkflowVersion) *service.VersionService {
	t.Helper()
	vRepo := mocks.NewMockWorkflowVersionRepository(gomock.NewController(t))
	vRepo.EXPECT().GetByID(gomock.Any(), v.TenantID, v.ID).Return(v, nil).AnyTimes()
	return service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: bpmn_compiler.New()})
}

func publishedGolden(t *testing.T) *domain.WorkflowVersion {
	t.Helper()
	got := publishWithLibrary(t, goldenMainBPMN, map[libraryKey]*domain.ModuleVersion{
		{"Process_Review", 0}: libraryVersion(uuid.New(), "Process_Review", 2, goldenReviewBPMN),
		{"Process_Check", 0}:  libraryVersion(uuid.New(), "Process_Check", 1, goldenCheckBPMN),
	})
	if got.err != nil {
		t.Fatalf("publish: %v", got.err)
	}
	n := int32(1)
	return &domain.WorkflowVersion{
		ID: uuid.New(), WorkflowID: uuid.New(), TenantID: uuid.New(), Status: domain.VersionStatusPublished,
		VersionNumber: &n, CompiledPlanJSON: &got.stored,
	}
}

// TestVersionNodes_LibraryCallingWorkflow reads the nodes of the golden
// library-calling workflow as publish stores it: every human task of the
// expanded main plan, module tasks included, keyed as the Execution Service
// keys its tasks and override_map.
func TestVersionNodes_LibraryCallingWorkflow(t *testing.T) {
	v := publishedGolden(t)
	got, err := nodesService(t, v).Nodes(context.Background(), v.TenantID, v.WorkflowID, v.ID)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}

	const eng, ops = "2200a5ae-2466-54f7-a1d8-243106e05705", "92fd92cf-4b2c-58dc-bf98-f3d0af729876"
	want := []service.PlanNode{
		{Key: "CA_Eng::Process_Review/Review_Task", NodeID: "Review_Task", Name: "Review", StageType: "review", Role: "reviewer",
			DepartmentID: "CA_Eng::Process_Review", DepartmentLabel: "Engineering", IAMDepartmentID: eng,
			DefaultUserIDs: []string{"018f2d3c-0000-7000-8000-0000000000a1"}},
		{Key: "CA_Eng::Review_Check::Process_Check/Check_Task", NodeID: "Check_Task", Name: "Check", StageType: "review", Role: "checker",
			DepartmentID: "CA_Eng::Review_Check::Process_Check", DepartmentLabel: "Engineering", IAMDepartmentID: eng,
			DefaultUserIDs: []string{"018f2d3c-0000-7000-8000-0000000000c1"}},
		{Key: "CA_Ops::Process_Review/Review_Task", NodeID: "Review_Task", Name: "Review", StageType: "review", Role: "reviewer",
			DepartmentID: "CA_Ops::Process_Review", DepartmentLabel: "Ops", IAMDepartmentID: ops,
			DefaultUserIDs: []string{"018f2d3c-0000-7000-8000-0000000000b2"}},
		{Key: "CA_Ops::Review_Check::Process_Check/Check_Task", NodeID: "Check_Task", Name: "Check", StageType: "review", Role: "checker",
			DepartmentID: "CA_Ops::Review_Check::Process_Check", DepartmentLabel: "Ops", IAMDepartmentID: ops,
			DefaultUserIDs: []string{"018f2d3c-0000-7000-8000-0000000000c2"}},
	}
	if !reflect.DeepEqual(got.Nodes, want) {
		t.Errorf("nodes:\n got %+v\nwant %+v", got.Nodes, want)
	}
	if got.Version != v {
		t.Error("the read does not return the version it read")
	}
}

// TestVersionNodes_KeysAreTheExecutionServiceKeys requires every node key to be
// one the Execution Service computes over workflow-models' golden, which its
// own tests run.
func TestVersionNodes_KeysAreTheExecutionServiceKeys(t *testing.T) {
	var golden dsl.CompiledCollaboration
	if err := json.Unmarshal(dsltest.LibraryCalls(), &golden); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	expanded, err := dsl.ExpandCalls(&golden, domain.MaxPlanStages)
	if err != nil {
		t.Fatalf("expand golden: %v", err)
	}
	executionKeys := map[string]bool{}
	for _, d := range expanded.Plans[0].Departments {
		for i := range d.Stages {
			if d.Stages[i].CreatesHumanTask() {
				executionKeys[dsl.NodeKey(d.ID, &d.Stages[i])] = true
			}
		}
	}

	v := publishedGolden(t)
	got, err := nodesService(t, v).Nodes(context.Background(), v.TenantID, v.WorkflowID, v.ID)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(got.Nodes) != len(executionKeys) {
		t.Fatalf("%d nodes, the golden has %d human tasks", len(got.Nodes), len(executionKeys))
	}
	for _, n := range got.Nodes {
		if !executionKeys[n.Key] {
			t.Errorf("node key %q is not one the Execution Service computes", n.Key)
		}
	}
}

func TestVersionNodes_DraftIsNotPublished(t *testing.T) {
	v := &domain.WorkflowVersion{ID: uuid.New(), WorkflowID: uuid.New(), TenantID: uuid.New(), Status: domain.VersionStatusDraft}
	_, err := nodesService(t, v).Nodes(context.Background(), v.TenantID, v.WorkflowID, v.ID)
	if !errors.Is(err, domain.ErrVersionNotPublished) {
		t.Errorf("err = %v, want ErrVersionNotPublished", err)
	}
}

func TestVersionNodes_ArchivedVersionIsRead(t *testing.T) {
	v := publishedGolden(t)
	v.Status = domain.VersionStatusArchived
	got, err := nodesService(t, v).Nodes(context.Background(), v.TenantID, v.WorkflowID, v.ID)
	if err != nil || len(got.Nodes) != 4 {
		t.Fatalf("Nodes = %v, %v; want the 4 nodes it was published with", got, err)
	}
}

func TestVersionNodes_VersionOfAnotherWorkflowIsNotFound(t *testing.T) {
	v := publishedGolden(t)
	_, err := nodesService(t, v).Nodes(context.Background(), v.TenantID, uuid.New(), v.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestVersionNodes_UnreadableStoredPlanIsAnError(t *testing.T) {
	v := publishedGolden(t)
	broken := `{"main_plan":"x","plans":[]}`
	v.CompiledPlanJSON = &broken
	if _, err := nodesService(t, v).Nodes(context.Background(), v.TenantID, v.WorkflowID, v.ID); err == nil {
		t.Error("a stored plan that does not expand gave no error")
	}
}

// TestVersionNodes_SkipsAutomaticStages: a node is a task a person completes.
func TestVersionNodes_SkipsAutomaticStages(t *testing.T) {
	plan, err := json.Marshal(dsl.CompiledCollaboration{MainPlan: "main", SchemaVersion: dsl.CurrentSchemaVersion, Plans: []*dsl.CompiledPlan{{
		Name: "main",
		Departments: []dsl.DepartmentDef{
			{ID: "Eng", Label: "Engineering", Stages: []dsl.StageDef{
				{Type: "prep", NodeID: "T1", Role: "L2"},
				{Type: "send_task", NodeID: "S1"},
				{Type: "connector", NodeID: "C1", ConnectorType: "storage"},
			}},
		},
		Execution: dsl.ExecutionPlan{Steps: []dsl.ExecutionStep{{Sequential: []string{"Eng"}}}},
	}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	stored := string(plan)
	v := &domain.WorkflowVersion{ID: uuid.New(), WorkflowID: uuid.New(), TenantID: uuid.New(), Status: domain.VersionStatusPublished, CompiledPlanJSON: &stored}
	got, err := nodesService(t, v).Nodes(context.Background(), v.TenantID, v.WorkflowID, v.ID)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].Key != "Eng/T1" {
		t.Errorf("nodes = %+v, want only Eng/T1", got.Nodes)
	}
}

func TestVersionNodes_ReadFailureIsReturned(t *testing.T) {
	vRepo := mocks.NewMockWorkflowVersionRepository(gomock.NewController(t))
	boom := errors.New("connection reset")
	vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, boom)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: bpmn_compiler.New()})
	if _, err := svc.Nodes(context.Background(), uuid.New(), uuid.New(), uuid.New()); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the repository's error", err)
	}
}

// TestVersionNodes_ListsTheWaitsOnlyAnOutsidePartyCompletes pins what Tender
// reads to answer an outside party: the receive task and the message boundary
// that no send task of the plan feeds, keyed as the Execution Service
// addresses them, while a receive task a send task feeds is not listed.
func TestVersionNodes_ListsTheWaitsOnlyAnOutsidePartyCompletes(t *testing.T) {
	collab := dsl.CompiledCollaboration{
		MainPlan: "main", SchemaVersion: dsl.CurrentSchemaVersion,
		Plans: []*dsl.CompiledPlan{{
			Name: "main",
			Departments: []dsl.DepartmentDef{{ID: "Ops", Stages: []dsl.StageDef{
				{Type: "send_task", NodeID: "Send_Ack", Extras: map[string]string{"message": "ack"}},
				{Type: "receive_task", NodeID: "Receive_Ack", Extras: map[string]string{"message": "ack"}},
				{Type: "receive_task", NodeID: "Receive_Bid", Extras: map[string]string{"message": "bid-received"}},
				{Type: "prep", NodeID: "Prepare", BoundaryMessage: &dsl.MessagePath{MessageName: "withdraw", BoundaryID: "Boundary_Withdraw", Interrupting: true, Terminates: true}},
			}}},
		}},
	}
	raw, err := json.Marshal(collab)
	if err != nil {
		t.Fatal(err)
	}
	stored := string(raw)
	n := int32(1)
	v := &domain.WorkflowVersion{
		ID: uuid.New(), WorkflowID: uuid.New(), TenantID: uuid.New(), Status: domain.VersionStatusPublished,
		VersionNumber: &n, CompiledPlanJSON: &stored,
	}

	got, err := nodesService(t, v).Nodes(context.Background(), v.TenantID, v.WorkflowID, v.ID)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	want := []service.PlanWait{
		{Key: "Ops/Receive_Bid", Kind: service.WaitReceiveTask, Message: "bid-received"},
		{Key: "Ops/Prepare#Boundary_Withdraw", Kind: service.WaitMessageBoundary, Message: "withdraw"},
	}
	if !reflect.DeepEqual(got.Waits, want) {
		t.Errorf("waits:\n got %+v\nwant %+v", got.Waits, want)
	}
}
