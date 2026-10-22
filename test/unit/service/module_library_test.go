package service_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

//go:embed testdata/library-caller.bpmn
var libraryCallerBPMN string

//go:embed testdata/library-module-review.bpmn
var libraryReviewBPMN string

//go:embed testdata/library-module-review-calls-check.bpmn
var libraryReviewCallsCheckBPMN string

//go:embed testdata/library-module-check.bpmn
var libraryCheckBPMN string

// libraryVersion is a published version of a library module.
func libraryVersion(moduleID uuid.UUID, processID string, number int32, bpmnXML string) *domain.ModuleVersion {
	n := number
	return &domain.ModuleVersion{
		ID: uuid.New(), ModuleID: moduleID, ProcessID: processID, VersionNumber: &n,
		Status: domain.VersionStatusPublished, BPMNXML: bpmnXML,
	}
}

type libraryKey struct {
	processID string
	version   int32
}

// expectLibrary makes the module version repository resolve each key to its
// version, and anything else to not found.
func expectLibrary(repo *mocks.MockModuleVersionRepository, tenantID uuid.UUID, library map[libraryKey]*domain.ModuleVersion) {
	repo.EXPECT().Resolve(gomock.Any(), tenantID, gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ uuid.UUID, processID string, version int32) (*domain.ModuleVersion, error) {
			if v, ok := library[libraryKey{processID, version}]; ok {
				return v, nil
			}
			return nil, domain.ErrNotFound
		}).AnyTimes()
}

type libraryPublish struct {
	stored string
	refs   []domain.ModuleRef
	err    error
}

// publishWithLibrary publishes bpmnXML through the real compiler, resolving
// its library calls from library.
func publishWithLibrary(t *testing.T, bpmnXML string, library map[libraryKey]*domain.ModuleVersion) libraryPublish {
	t.Helper()
	ctrl := gomock.NewController(t)
	tx := mocks.NewMockTransactor(ctrl)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	assignees := mocks.NewMockAssigneeRepository(ctrl)
	modules := mocks.NewMockModuleVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: tx, Workflows: wfRepo, Versions: vRepo,
		Assignees: assignees, Compiler: bpmn_compiler.New(), ModuleVersions: modules,
	})

	tenantID, userID, wfID, vID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	expectLibrary(modules, tenantID, library)
	var out libraryPublish
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(&domain.WorkflowVersion{
		ID: vID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusDraft, BPMNXML: bpmnXML,
	}, nil).AnyTimes()
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil).AnyTimes()
	vRepo.EXPECT().NextVersionNumber(gomock.Any(), tenantID, wfID).Return(int32(1), nil).AnyTimes()
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }).AnyTimes()
	vRepo.EXPECT().Publish(gomock.Any(), tenantID, vID, gomock.Any(), int32(1), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ uuid.UUID, _ int64, _ int32, compiledPlanJSON, _ string, refs []domain.ModuleRef) error {
			out.stored, out.refs = compiledPlanJSON, refs
			return nil
		}).AnyTimes()
	assignees.EXPECT().DeleteByVersion(gomock.Any(), tenantID, vID).Return(nil).AnyTimes()
	assignees.EXPECT().BulkInsert(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	wfRepo.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, &vID).Return(nil).AnyTimes()

	_, out.err = svc.Publish(context.Background(), tenantID, userID, wfID, vID, false)
	return out
}

func storedCollaboration(t *testing.T, stored string) *dsl.CompiledCollaboration {
	t.Helper()
	var collab dsl.CompiledCollaboration
	if err := json.Unmarshal([]byte(stored), &collab); err != nil {
		t.Fatalf("stored plan does not decode: %v", err)
	}
	return &collab
}

func planNames(c *dsl.CompiledCollaboration) []string {
	var names []string
	for _, p := range c.Plans {
		names = append(names, p.Name)
	}
	return names
}

func TestPublish_ResolvesPinnedAndLatestLibraryVersions(t *testing.T) {
	moduleID := uuid.New()
	v3 := libraryVersion(moduleID, "Process_Review", 3, libraryReviewBPMN)
	v4 := libraryVersion(moduleID, "Process_Review", 4, libraryReviewBPMN)
	got := publishWithLibrary(t, libraryCallerBPMN, map[libraryKey]*domain.ModuleVersion{
		{"Process_Review", 3}: v3,
		{"Process_Review", 0}: v4,
	})
	if got.err != nil {
		t.Fatalf("publish: %v", got.err)
	}

	collab := storedCollaboration(t, got.stored)
	if names := planNames(collab); !reflect.DeepEqual(names, []string{"Main", "Process_Review@v3", "Process_Review@v4"}) {
		t.Errorf("plans = %v, want the main plan and one plan per resolved version", names)
	}
	wantRefs := []domain.ModuleRef{
		{ModuleID: moduleID, VersionID: v3.ID, ProcessID: "Process_Review", VersionNumber: 3},
		{ModuleID: moduleID, VersionID: v4.ID, ProcessID: "Process_Review", VersionNumber: 4},
	}
	if !reflect.DeepEqual(got.refs, wantRefs) {
		t.Errorf("module refs = %+v, want %+v", got.refs, wantRefs)
	}
	expanded, err := dsl.ExpandCalls(collab, domain.MaxPlanStages)
	if err != nil {
		t.Fatalf("stored plan does not expand: %v", err)
	}
	// Node keys name the module by its process id, not its version, so a call
	// moving from v3 to v4 keeps its tasks' keys.
	for id, iam := range map[string]string{
		"CA_1::Process_Review": "2200a5ae-2466-54f7-a1d8-243106e05705",
		"CA_2::Process_Review": "92fd92cf-4b2c-58dc-bf98-f3d0af729876",
	} {
		found := false
		for _, d := range expanded.Plans[0].Departments {
			found = found || (d.ID == id && d.IAMDepartmentID == iam)
		}
		if !found {
			t.Errorf("no department %s running in the caller's %s", id, iam)
		}
	}
}

func TestPublish_ModuleCallingAModuleResolvesBoth(t *testing.T) {
	review := libraryVersion(uuid.New(), "Process_Review", 3, libraryReviewCallsCheckBPMN)
	check := uuid.New()
	got := publishWithLibrary(t, libraryCallerBPMN, map[libraryKey]*domain.ModuleVersion{
		{"Process_Review", 3}: review,
		{"Process_Review", 0}: review,
		{"Process_Check", 0}:  libraryVersion(check, "Process_Check", 1, libraryCheckBPMN),
	})
	if got.err != nil {
		t.Fatalf("publish: %v", got.err)
	}
	if names := planNames(storedCollaboration(t, got.stored)); !reflect.DeepEqual(names, []string{"Main", "Process_Check@v1", "Process_Review@v3"}) {
		t.Errorf("plans = %v, want the review module once and the check module it calls", names)
	}
	if len(got.refs) != 2 {
		t.Errorf("module refs = %+v, want the two module versions", got.refs)
	}
}

func TestPublish_UnresolvableLibraryCallIsRefused(t *testing.T) {
	got := publishWithLibrary(t, libraryCallerBPMN, map[libraryKey]*domain.ModuleVersion{
		{"Process_Review", 0}: libraryVersion(uuid.New(), "Process_Review", 4, libraryReviewBPMN),
	})
	var vf *domain.ValidationFailedError
	if !errors.As(got.err, &vf) {
		t.Fatalf("publish err = %v, want a ValidationFailedError", got.err)
	}
	var found bool
	for _, e := range vf.Errors {
		if e.Code == domain.BPMNErrUnresolvedCalledElement && e.NodeID == "CA_1" &&
			strings.Contains(e.Message, "Process_Review") && strings.Contains(e.Message, "3") {
			found = true
		}
	}
	if !found {
		t.Errorf("errors = %+v, want UNRESOLVED_CALLED_ELEMENT on CA_1 naming Process_Review version 3", vf.Errors)
	}
	if got.stored != "" {
		t.Error("a refused publish stored a plan")
	}
}

func TestValidationService_ResolvesLibraryModules(t *testing.T) {
	ctrl := gomock.NewController(t)
	modules := mocks.NewMockModuleVersionRepository(ctrl)
	tenantID := uuid.New()
	expectLibrary(modules, tenantID, map[libraryKey]*domain.ModuleVersion{
		{"Process_Review", 3}: libraryVersion(uuid.New(), "Process_Review", 3, libraryReviewBPMN),
	})
	svc := service.NewValidationService(service.ValidationDeps{Compiler: bpmn_compiler.New(), ModuleVersions: modules})

	isValid, errs, err := svc.Validate(context.Background(), tenantID, libraryCallerBPMN)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if isValid {
		t.Error("is_valid = true, but CA_2's latest Process_Review does not resolve")
	}
	if len(errs) != 1 || errs[0].Code != domain.BPMNErrUnresolvedCalledElement || errs[0].NodeID != "CA_2" {
		t.Errorf("errors = %+v, want one UNRESOLVED_CALLED_ELEMENT on CA_2", errs)
	}
}

// openLaneModule is a tenant module with one open lane and a task that names
// no user, both of which are fine for a module.
var openLaneModule = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="Def_Open" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_Open" isExecutable="true">
    <bpmn:laneSet id="LS"><bpmn:lane id="L1" name="Sender"><bpmn:flowNodeRef>O_Start</bpmn:flowNodeRef><bpmn:flowNodeRef>O_Task</bpmn:flowNodeRef><bpmn:flowNodeRef>O_End</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="O_Start"/>
    <bpmn:userTask id="O_Task"><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparator"/></bpmn:extensionElements></bpmn:userTask>
    <bpmn:endEvent id="O_End"/>
    <bpmn:sequenceFlow id="O_F1" sourceRef="O_Start" targetRef="O_Task"/>
    <bpmn:sequenceFlow id="O_F2" sourceRef="O_Task" targetRef="O_End"/>
  </bpmn:process>
</bpmn:definitions>`

func newLibraryModuleService(ctrl *gomock.Controller) (*service.ModuleService, moduleMocks) {
	m := moduleMocks{
		tx:               mocks.NewMockTransactor(ctrl),
		globalTx:         mocks.NewMockTransactor(ctrl),
		modules:          mocks.NewMockModuleRepository(ctrl),
		globalModules:    mocks.NewMockModuleRepository(ctrl),
		versions:         mocks.NewMockModuleVersionRepository(ctrl),
		globalVersions:   mocks.NewMockModuleVersionRepository(ctrl),
		workflowVersions: mocks.NewMockWorkflowVersionRepository(ctrl),
	}
	svc := service.NewModuleService(service.ModuleDeps{
		Transactor: m.tx, GlobalTransactor: m.globalTx,
		Modules: m.modules, GlobalModules: m.globalModules,
		Versions: m.versions, GlobalVersions: m.globalVersions,
		WorkflowVersions: m.workflowVersions,
		Compiler:         bpmn_compiler.New(),
	})
	return svc, m
}

func TestModuleService_Create_OpenLaneModuleWithoutDefaultUsers(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newLibraryModuleService(ctrl)
	tenantID := uuid.New()
	passthrough(m.tx)
	m.modules.EXPECT().ProcessIDInUse(gomock.Any(), &tenantID, "Process_Open").Return(false, nil)
	var created *domain.Module
	m.modules.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mod *domain.Module) error {
		created = mod
		return nil
	})
	m.versions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	if _, _, err := svc.Create(context.Background(), tenantID, uuid.New(), service.CreateModuleReq{Name: "Open", BPMNXML: openLaneModule}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if created == nil || created.ProcessID != "Process_Open" {
		t.Errorf("created module = %+v, want process id Process_Open recorded", created)
	}
}

func TestModuleService_Create_ProcessIDInUseIsAConflict(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newLibraryModuleService(ctrl)
	tenantID := uuid.New()
	m.modules.EXPECT().ProcessIDInUse(gomock.Any(), &tenantID, "Process_Open").Return(true, nil)

	_, _, err := svc.Create(context.Background(), tenantID, uuid.New(), service.CreateModuleReq{Name: "Open", BPMNXML: openLaneModule})
	if !errors.Is(err, domain.ErrModuleProcessIDTaken) {
		t.Errorf("err = %v, want ErrModuleProcessIDTaken", err)
	}
}

func TestModuleService_Create_GlobalModuleNamingAUserIsRefused(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, _ := newLibraryModuleService(ctrl)
	named := strings.Replace(openLaneModule, `candidateGroups="preparator"/>`, `candidateGroups="preparator" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>`, 1)

	_, _, err := svc.Create(context.Background(), uuid.New(), uuid.New(), service.CreateModuleReq{Name: "Open", BPMNXML: named, Scope: domain.ScopeGlobal})
	var vf *domain.ValidationFailedError
	if !errors.As(err, &vf) || vf.Errors[0].Code != domain.BPMNErrInvalidCandidateUser {
		t.Errorf("err = %v, want INVALID_CANDIDATE_USER: a global module names no user", err)
	}
}

func TestModuleService_AddVersion_MustKeepTheModulesProcessID(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newLibraryModuleService(ctrl)
	tenantID, moduleID := uuid.New(), uuid.New()
	m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).Return(&domain.Module{ID: moduleID, Scope: domain.ScopeTenant, TenantID: &tenantID, ProcessID: "Process_Other"}, nil)

	_, err := svc.AddVersion(context.Background(), tenantID, uuid.New(), moduleID, service.AddModuleVersionReq{BPMNXML: &openLaneModule})
	if !errors.Is(err, domain.ErrModuleProcessIDChanged) {
		t.Errorf("err = %v, want ErrModuleProcessIDChanged", err)
	}
}

func TestModuleService_Usages_SayWhichAreBehind(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newLibraryModuleService(ctrl)
	tenantID, moduleID, activeID := uuid.New(), uuid.New(), uuid.New()
	three := int32(3)
	m.modules.EXPECT().GetByID(gomock.Any(), tenantID, moduleID).Return(&domain.Module{ID: moduleID, ActiveVersionID: &activeID}, nil)
	m.versions.EXPECT().GetByID(gomock.Any(), tenantID, activeID).Return(&domain.ModuleVersion{ID: activeID, ModuleID: moduleID, VersionNumber: &three}, nil)
	usages := []*domain.ModuleUsage{{WorkflowKey: "old", ModuleVersionNumber: 2}, {WorkflowKey: "current", ModuleVersionNumber: 3}}
	m.workflowVersions.EXPECT().ListModuleUsages(gomock.Any(), tenantID, moduleID).Return(usages, nil)

	got, err := svc.Usages(context.Background(), tenantID, moduleID)
	if err != nil {
		t.Fatalf("usages: %v", err)
	}
	if len(got) != 2 || !got[0].Behind || got[1].Behind {
		t.Errorf("usages = %+v, want old behind and current not", got)
	}
}

// withoutUsers strips every default user, leaving CANDIDATE_USERS_EMPTY
// warnings.
func withoutUsers(bpmnXML string) string {
	return regexp.MustCompile(` candidateUsers="[^"]*"`).ReplaceAllString(bpmnXML, "")
}

func TestWorkflowService_Create_ResolvesLibraryCallsAndWarningsDoNotBlock(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	modules := mocks.NewMockModuleVersionRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{PlanLimits: stdPlanLimits(),
		Transactor: txPassthrough(ctrl), Workflows: wfRepo, Versions: vRepo,
		Compiler: bpmn_compiler.New(), ModuleVersions: modules,
	})
	tenantID := uuid.New()
	review := withoutUsers(libraryReviewBPMN)
	expectLibrary(modules, tenantID, map[libraryKey]*domain.ModuleVersion{
		{"Process_Review", 3}: libraryVersion(uuid.New(), "Process_Review", 3, review),
		{"Process_Review", 0}: libraryVersion(uuid.New(), "Process_Review", 4, review),
	})
	wfRepo.EXPECT().CountByTenant(gomock.Any(), tenantID).Return(int64(0), nil)
	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	if _, _, err := svc.Create(context.Background(), tenantID, uuid.New(), "key", "name", "", libraryCallerBPMN, "starter"); err != nil {
		t.Fatalf("create: %v", err)
	}
}

func TestStarterService_Create_ResolvesLibraryCallsForItsScope(t *testing.T) {
	ctrl := gomock.NewController(t)
	starters := mocks.NewMockStarterTemplateRepository(ctrl)
	globalStarters := mocks.NewMockStarterTemplateRepository(ctrl)
	modules := mocks.NewMockModuleVersionRepository(ctrl)
	globalModules := mocks.NewMockModuleVersionRepository(ctrl)
	svc := service.NewStarterService(service.StarterDeps{
		Starters: starters, GlobalStarters: globalStarters, Compiler: bpmn_compiler.New(),
		ModuleVersions: modules, GlobalModuleVersions: globalModules,
	})
	tenantID := uuid.New()
	library := map[libraryKey]*domain.ModuleVersion{
		{"Process_Review", 3}: libraryVersion(uuid.New(), "Process_Review", 3, libraryReviewBPMN),
		{"Process_Review", 0}: libraryVersion(uuid.New(), "Process_Review", 4, libraryReviewBPMN),
	}
	expectLibrary(modules, tenantID, library)
	expectLibrary(globalModules, uuid.Nil, library)
	starters.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	globalStarters.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	for _, scope := range []domain.CatalogScope{domain.ScopeTenant, domain.ScopeGlobal} {
		if _, err := svc.Create(context.Background(), tenantID, uuid.New(), service.CreateStarterReq{Name: "s", BPMNXML: libraryCallerBPMN, Scope: scope}); err != nil {
			t.Errorf("%s starter: %v", scope, err)
		}
	}
}

// checkCallsReview is Process_Check calling Process_Review at its latest
// version.
var checkCallsReview = strings.NewReplacer("Process_Check", "Process_Review", "Process_Review", "Process_Check", "Review_", "CheckCall_").
	Replace(libraryReviewCallsCheckBPMN)

func TestModuleService_Create_ACallBackToItselfAtLatestIsACycle(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newLibraryModuleService(ctrl)
	tenantID := uuid.New()
	expectLibrary(m.versions, tenantID, map[libraryKey]*domain.ModuleVersion{
		{"Process_Check", 0}:  libraryVersion(uuid.New(), "Process_Check", 1, checkCallsReview),
		{"Process_Review", 0}: libraryVersion(uuid.New(), "Process_Review", 1, libraryReviewBPMN),
	})

	_, _, err := svc.Create(context.Background(), tenantID, uuid.New(), service.CreateModuleReq{Name: "Review", BPMNXML: libraryReviewCallsCheckBPMN})
	var vf *domain.ValidationFailedError
	if !errors.As(err, &vf) {
		t.Fatalf("err = %v, want a ValidationFailedError", err)
	}
	got := vf.Errors[0]
	if got.Code != domain.BPMNErrCalledElementCycle || got.NodeID != "Review_Check" || !strings.Contains(got.Message, "Process_Check") {
		t.Errorf("errors = %+v, want CALLED_ELEMENT_CYCLE on Review_Check, through Process_Check", vf.Errors)
	}
}

func TestModuleService_Create_ACallBackToAnEarlierPinnedVersionOfItselfIsNotACycle(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newLibraryModuleService(ctrl)
	tenantID := uuid.New()
	pinnedBack := strings.Replace(checkCallsReview, `processId="Process_Review"`, `processId="Process_Review" bindingType="versionTag" versionTag="1"`, 1)
	expectLibrary(m.versions, tenantID, map[libraryKey]*domain.ModuleVersion{
		{"Process_Check", 0}:  libraryVersion(uuid.New(), "Process_Check", 1, pinnedBack),
		{"Process_Review", 1}: libraryVersion(uuid.New(), "Process_Review", 1, withoutUsers(libraryCheckBPMN)),
	})
	passthrough(m.tx)
	m.modules.EXPECT().ProcessIDInUse(gomock.Any(), &tenantID, "Process_Review").Return(false, nil)
	m.modules.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	m.versions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	if _, _, err := svc.Create(context.Background(), tenantID, uuid.New(), service.CreateModuleReq{Name: "Review", BPMNXML: libraryReviewCallsCheckBPMN}); err != nil {
		t.Fatalf("create: %v", err)
	}
}

func TestPublish_AProblemInsideAModuleIsReportedOnTheWorkflowsCall(t *testing.T) {
	review := libraryVersion(uuid.New(), "Process_Review", 3, libraryReviewCallsCheckBPMN)
	got := publishWithLibrary(t, libraryCallerBPMN, map[libraryKey]*domain.ModuleVersion{
		{"Process_Review", 3}: review,
		{"Process_Review", 0}: review,
	})
	var vf *domain.ValidationFailedError
	if !errors.As(got.err, &vf) {
		t.Fatalf("publish err = %v, want a ValidationFailedError", got.err)
	}
	e := vf.Errors[0]
	if e.Code != domain.BPMNErrUnresolvedCalledElement || e.NodeID != "CA_1" ||
		!strings.Contains(e.Message, `"Process_Review" version 3`) || !strings.Contains(e.Message, "Process_Check") {
		t.Errorf("errors = %+v, want UNRESOLVED_CALLED_ELEMENT on CA_1, naming Process_Review version 3 and the missing Process_Check", vf.Errors)
	}
}

func TestValidationService_ValidateVersion_APublishedVersionUsesItsSnapshot(t *testing.T) {
	ctrl := gomock.NewController(t)
	modules := mocks.NewMockModuleVersionRepository(ctrl)
	svc := service.NewValidationService(service.ValidationDeps{Compiler: bpmn_compiler.New(), ModuleVersions: modules})
	tenantID, moduleID := uuid.New(), uuid.New()
	v3 := libraryVersion(moduleID, "Process_Review", 3, libraryReviewBPMN)
	v4 := libraryVersion(moduleID, "Process_Review", 4, libraryReviewBPMN)
	v3.Status, v4.Status = domain.VersionStatusArchived, domain.VersionStatusArchived
	modules.EXPECT().GetByID(gomock.Any(), tenantID, v3.ID).Return(v3, nil).AnyTimes()
	modules.EXPECT().GetByID(gomock.Any(), tenantID, v4.ID).Return(v4, nil).AnyTimes()
	published := &domain.WorkflowVersion{
		TenantID: tenantID, Status: domain.VersionStatusPublished, BPMNXML: libraryCallerBPMN,
		ModuleRefs: []domain.ModuleRef{
			{ModuleID: moduleID, VersionID: v3.ID, ProcessID: "Process_Review", VersionNumber: 3},
			{ModuleID: moduleID, VersionID: v4.ID, ProcessID: "Process_Review", VersionNumber: 4},
		},
	}

	isValid, errs, err := svc.ValidateVersion(context.Background(), tenantID, published)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !isValid {
		t.Errorf("is_valid = false (%+v); a published version is judged against the module versions it was published with, archived or not", errs)
	}
}

// TestDiff_PublishedVersionWithAnUnreadablePlan_UsesItsOwnModuleVersions pins
// that a published version is recompiled against the module versions it was
// published with, not today's library, when its stored plan cannot be read.
func TestDiff_PublishedVersionWithAnUnreadablePlan_UsesItsOwnModuleVersions(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	modules := mocks.NewMockModuleVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: bpmn_compiler.New(), ModuleVersions: modules})

	tenantID, wfID, baseID, targetID, reviewID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	v3 := libraryVersion(reviewID, "Process_Review", 3, libraryReviewBPMN)
	expectLibrary(modules, tenantID, map[libraryKey]*domain.ModuleVersion{
		{"Process_Review", 3}: v3,
		{"Process_Review", 0}: libraryVersion(reviewID, "Process_Review", 4, libraryReviewCallsCheckBPMN),
		{"Process_Check", 0}:  libraryVersion(uuid.New(), "Process_Check", 1, libraryCheckBPMN),
	})
	modules.EXPECT().GetByID(gomock.Any(), tenantID, v3.ID).Return(v3, nil).AnyTimes()
	unreadable, one := "not valid json", int32(1)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(&domain.WorkflowVersion{
		ID: baseID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusPublished, VersionNumber: &one,
		BPMNXML: libraryCallerBPMN, CompiledPlanJSON: &unreadable,
		ModuleRefs: []domain.ModuleRef{{ModuleID: reviewID, VersionID: v3.ID, ProcessID: "Process_Review", VersionNumber: 3}},
	}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(&domain.WorkflowVersion{
		ID: targetID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusDraft, BPMNXML: libraryCallerBPMN,
	}, nil)

	result, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if result.ChangeType != "STRUCTURAL" {
		t.Errorf("change type = %s, want STRUCTURAL: the published version ran Process_Review v3 at both calls, the draft takes v4 at one", result.ChangeType)
	}
}

// TestDiff_DraftThatIsNotYetPublishable_StillDiffs pins that the diff compiles
// a draft without publish's own refusals: a draft may be saved before every
// task has its user, and it can still be compared with the active version.
func TestDiff_DraftThatIsNotYetPublishable_StillDiffs(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	modules := mocks.NewMockModuleVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: bpmn_compiler.New(), ModuleVersions: modules})

	tenantID, wfID, baseID, targetID, reviewID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	v3 := libraryVersion(reviewID, "Process_Review", 3, libraryReviewBPMN)
	expectLibrary(modules, tenantID, map[libraryKey]*domain.ModuleVersion{
		{"Process_Review", 3}: v3,
		{"Process_Review", 0}: libraryVersion(reviewID, "Process_Review", 4, libraryReviewCallsCheckBPMN),
		{"Process_Check", 0}:  libraryVersion(uuid.New(), "Process_Check", 1, goldenCheckBPMN),
	})
	modules.EXPECT().GetByID(gomock.Any(), tenantID, v3.ID).Return(v3, nil).AnyTimes()
	unreadable, one := "not valid json", int32(1)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, baseID).Return(&domain.WorkflowVersion{
		ID: baseID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusPublished, VersionNumber: &one,
		BPMNXML: libraryCallerBPMN, CompiledPlanJSON: &unreadable,
		ModuleRefs: []domain.ModuleRef{{ModuleID: reviewID, VersionID: v3.ID, ProcessID: "Process_Review", VersionNumber: 3}},
	}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, targetID).Return(&domain.WorkflowVersion{
		ID: targetID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusDraft, BPMNXML: libraryCallerBPMN,
	}, nil)

	result, err := svc.Diff(context.Background(), tenantID, wfID, baseID, targetID)
	if err != nil {
		t.Fatalf("Diff: %v; a draft whose nested Check_Task names nobody yet must still diff", err)
	}
	if result.ChangeType != "STRUCTURAL" {
		t.Errorf("change type = %s, want STRUCTURAL", result.ChangeType)
	}
}
