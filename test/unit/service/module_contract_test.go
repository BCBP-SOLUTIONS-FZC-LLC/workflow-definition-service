package service_test

import (
	"context"
	_ "embed"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

//go:embed testdata/module-contract-package.bpmn
var contractPackageBPMN string

//go:embed testdata/module-contract-outer.bpmn
var contractOuterBPMN string

//go:embed testdata/module-contract-fixed.bpmn
var contractFixedBPMN string

//go:embed testdata/module-contract-swap.bpmn
var contractSwapBPMN string

//go:embed testdata/module-contract-pair.bpmn
var contractPairBPMN string

// contractService reads module versions from the tenant pool, and resolves a
// module's library calls from library on the pool its scope uses.
func contractService(t *testing.T, scope domain.CatalogScope, v *domain.ModuleVersion, library map[libraryKey]*domain.ModuleVersion) (*service.ModuleService, uuid.UUID) {
	t.Helper()
	ctrl := gomock.NewController(t)
	versions := mocks.NewMockModuleVersionRepository(ctrl)
	globalVersions := mocks.NewMockModuleVersionRepository(ctrl)
	tenantID := uuid.New()
	v.Scope = scope
	versions.EXPECT().GetByID(gomock.Any(), tenantID, v.ID).Return(v, nil).AnyTimes()
	if scope == domain.ScopeGlobal {
		expectLibrary(globalVersions, uuid.Nil, library)
	} else {
		expectLibrary(versions, tenantID, library)
	}
	return service.NewModuleService(service.ModuleDeps{
		Versions: versions, GlobalVersions: globalVersions, Compiler: bpmn_compiler.New(),
	}), tenantID
}

var checkLibrary = map[libraryKey]*domain.ModuleVersion{
	{"Process_Check", 0}: libraryVersion(uuid.New(), "Process_Check", 1, goldenCheckBPMN),
}

// TestModuleContract_OpenAndFixedSlots reads a module with an open lane, a
// fixed lane and a call of a laneless module bound by default to the call's
// lane: the slots a caller binds, and every task it can name a user for, by
// the key its Assignees takes.
func TestModuleContract_OpenAndFixedSlots(t *testing.T) {
	v := libraryVersion(uuid.New(), "Process_Package", 3, contractPackageBPMN)
	svc, tenantID := contractService(t, domain.ScopeTenant, v, checkLibrary)

	got, err := svc.Contract(context.Background(), tenantID, v.ModuleID, v.ID)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	wantSlots := []service.ModuleSlot{
		{ID: "Author", Label: "Author", Open: true},
		{ID: "Quality", Label: "Quality", IAMDepartmentID: "6f1c0e52-3b1d-5d7e-9a3c-0c4b8e2f1a77"},
	}
	if !reflect.DeepEqual(got.Slots, wantSlots) {
		t.Errorf("slots:\n got %+v\nwant %+v", got.Slots, wantSlots)
	}
	wantTasks := []service.ModuleTask{
		{AssigneeKey: "Pkg_Prep", NodeID: "Pkg_Prep", Name: "Prepare the package", Slot: "Author", StageType: "prep", Role: "L2"},
		{AssigneeKey: "Pkg_Approve", NodeID: "Pkg_Approve", Name: "Approve the package", Slot: "Quality", StageType: "approve", Role: "L4"},
		{AssigneeKey: "Pkg_Check::Check_Task", NodeID: "Check_Task", Name: "Check", Slot: "Author", StageType: "review", Role: "checker"},
	}
	if !reflect.DeepEqual(got.Tasks, wantTasks) {
		t.Errorf("tasks:\n got %+v\nwant %+v", got.Tasks, wantTasks)
	}
	if want := []string{"package-withdrawn"}; !reflect.DeepEqual(got.Messages, want) {
		t.Errorf("messages = %v, want %v", got.Messages, want)
	}
	if want := []string{"PACKAGE_REJECTED"}; !reflect.DeepEqual(got.ErrorCodes, want) {
		t.Errorf("error codes = %v, want %v", got.ErrorCodes, want)
	}
	if got.NamesUsers {
		t.Error("NamesUsers is true for a module whose tasks name no user")
	}
	if got.Version != v {
		t.Error("the read does not return the version it read")
	}
}

// TestModuleContract_LanelessModule reads the golden review module: one open
// slot named by its process id, a nested call's task by its path, and its own
// message, carried with it as a call brings it in.
func TestModuleContract_LanelessModule(t *testing.T) {
	v := libraryVersion(uuid.New(), "Process_Review", 2, goldenReviewBPMN)
	svc, tenantID := contractService(t, domain.ScopeTenant, v, checkLibrary)

	got, err := svc.Contract(context.Background(), tenantID, v.ModuleID, v.ID)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	if want := []service.ModuleSlot{{ID: "Process_Review", Label: "Process_Review", Open: true}}; !reflect.DeepEqual(got.Slots, want) {
		t.Errorf("slots = %+v, want %+v", got.Slots, want)
	}
	wantTasks := []service.ModuleTask{
		{AssigneeKey: "Review_Task", NodeID: "Review_Task", Name: "Review", Slot: "Process_Review", StageType: "review", Role: "reviewer",
			DefaultUserIDs: []string{"018f2d3c-0000-7000-8000-0000000000a1"}},
		{AssigneeKey: "Review_Check::Check_Task", NodeID: "Check_Task", Name: "Check", Slot: "Process_Review", StageType: "review", Role: "checker"},
	}
	if !reflect.DeepEqual(got.Tasks, wantTasks) {
		t.Errorf("tasks:\n got %+v\nwant %+v", got.Tasks, wantTasks)
	}
	if want := []string{"review-recalled"}; !reflect.DeepEqual(got.Messages, want) {
		t.Errorf("messages = %v, want %v", got.Messages, want)
	}
	if !got.NamesUsers {
		t.Error("NamesUsers is false for a module whose review task names a user")
	}
}

func TestModuleContract_GlobalModuleResolvesGlobalModules(t *testing.T) {
	v := libraryVersion(uuid.New(), "Process_Package", 1, contractPackageBPMN)
	svc, tenantID := contractService(t, domain.ScopeGlobal, v, checkLibrary)

	got, err := svc.Contract(context.Background(), tenantID, v.ModuleID, v.ID)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	if len(got.Tasks) != 3 {
		t.Errorf("%d tasks, want 3 with the global Process_Check's", len(got.Tasks))
	}
}

func TestModuleContract_VersionOfAnotherModuleIsNotFound(t *testing.T) {
	v := libraryVersion(uuid.New(), "Process_Package", 1, contractPackageBPMN)
	svc, tenantID := contractService(t, domain.ScopeTenant, v, checkLibrary)
	if _, err := svc.Contract(context.Background(), tenantID, uuid.New(), v.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestModuleContract_UnresolvableCallFailsValidation(t *testing.T) {
	v := libraryVersion(uuid.New(), "Process_Package", 1, contractPackageBPMN)
	svc, tenantID := contractService(t, domain.ScopeTenant, v, nil)
	var failed *domain.ValidationFailedError
	if _, err := svc.Contract(context.Background(), tenantID, v.ModuleID, v.ID); !errors.As(err, &failed) {
		t.Errorf("err = %v, want a ValidationFailedError naming the unresolved call", err)
	}
}

// TestModuleContract_CallOnAParallelBranch reads a module that runs a call on
// a parallel branch and a task inside a subProcess. The called module's only
// lane is fixed, so its task has no slot and keeps its own IAM department, and
// its connector task is not one a call names a user for.
func TestModuleContract_CallOnAParallelBranch(t *testing.T) {
	v := libraryVersion(uuid.New(), "Process_Outer", 1, contractOuterBPMN)
	svc, tenantID := contractService(t, domain.ScopeTenant, v, map[libraryKey]*domain.ModuleVersion{
		{"Process_Fixed", 0}: libraryVersion(uuid.New(), "Process_Fixed", 1, contractFixedBPMN),
	})

	got, err := svc.Contract(context.Background(), tenantID, v.ModuleID, v.ID)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	wantTasks := []service.ModuleTask{
		{AssigneeKey: "Out_Task", NodeID: "Out_Task", Name: "Draft", Slot: "Drafting", StageType: "prep", Role: "L2"},
		{AssigneeKey: "Sub_Task", NodeID: "Sub_Task", Name: "Finalise the draft", Slot: "Drafting", StageType: "approve", Role: "L4"},
		{AssigneeKey: "Out_CallFixed::Fx_Task", NodeID: "Fx_Task", Name: "Clear the terms",
			IAMDepartmentID: "0b8f3a51-6c2d-5e4f-8a7b-1c9d2e3f4a5b", StageType: "review", Role: "L3"},
	}
	if !reflect.DeepEqual(got.Tasks, wantTasks) {
		t.Errorf("tasks:\n got %+v\nwant %+v", got.Tasks, wantTasks)
	}
	if want := []service.ModuleSlot{{ID: "Drafting", Label: "Drafting", Open: true}, {ID: "Legal desk", Label: "Legal desk", Open: true}}; !reflect.DeepEqual(got.Slots, want) {
		t.Errorf("slots = %+v, want %+v", got.Slots, want)
	}
}

func TestModuleContract_VersionThatNoLongerCompilesFailsValidation(t *testing.T) {
	broken := strings.Replace(goldenCheckBPMN, `<bpmn:endEvent id="Check_End"/>`, "", 1)
	v := libraryVersion(uuid.New(), "Process_Check", 1, broken)
	svc, tenantID := contractService(t, domain.ScopeTenant, v, nil)
	var failed *domain.ValidationFailedError
	if _, err := svc.Contract(context.Background(), tenantID, v.ModuleID, v.ID); !errors.As(err, &failed) {
		t.Errorf("err = %v, want a ValidationFailedError", err)
	}
}

// TestModuleContract_DeptsBindsCalledSlotsCrosswise: a called module's two
// open slots, bound through Depts to the calling module's slots the other way
// round, run in the slot each is bound to.
func TestModuleContract_DeptsBindsCalledSlotsCrosswise(t *testing.T) {
	v := libraryVersion(uuid.New(), "Process_Swap", 1, contractSwapBPMN)
	svc, tenantID := contractService(t, domain.ScopeTenant, v, map[libraryKey]*domain.ModuleVersion{
		{"Process_Pair", 0}: libraryVersion(uuid.New(), "Process_Pair", 1, contractPairBPMN),
	})

	got, err := svc.Contract(context.Background(), tenantID, v.ModuleID, v.ID)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	slotOf := map[string]string{}
	for _, task := range got.Tasks {
		slotOf[task.AssigneeKey] = task.Slot
	}
	want := map[string]string{"Swap_Task": "Second", "Swap_Call::Pair_Send": "Second", "Swap_Call::Pair_Receive": "First"}
	if !reflect.DeepEqual(slotOf, want) {
		t.Errorf("slots by task = %v, want %v", slotOf, want)
	}
}
