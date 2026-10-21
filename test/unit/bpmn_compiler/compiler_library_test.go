package bpmn_compiler_test

import (
	"context"
	"errors"
	"fmt"
	"html"
	"reflect"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// pinned rewrites the call CA_1's calledElement to pin versionTag, or to the
// given raw attributes when versionTag starts with "attrs:".
func pinned(bpmnXML, processID, versionTag string) string {
	attrs := fmt.Sprintf(`processId=%q bindingType="versionTag" versionTag=%q`, processID, versionTag)
	if raw, ok := strings.CutPrefix(versionTag, "attrs:"); ok {
		attrs = raw
	}
	return strings.Replace(bpmnXML, fmt.Sprintf(`<zeebe:calledElement processId=%q/>`, processID), "<zeebe:calledElement "+attrs+"/>", 1)
}

func libraryModule(id string) string {
	return moduleDocument(moduleWithTask(id, validCandidateUser))
}

func TestModuleCalls_ListsCallsToProcessesOutsideTheDocument(t *testing.T) {
	bpmnXML := mainWithCalls([]callSite{
		{ID: "CA_1", Lane: "Engineering", Calls: "Process_Lib"},
		{ID: "CA_2", Lane: "Ops", Calls: "Process_Lib"},
		{ID: "CA_3", Lane: "Ops", Calls: "Process_Local"},
	}, moduleWithTask("Process_Local", validCandidateUser))
	bpmnXML = pinned(bpmnXML, "Process_Lib", "3")

	calls, issues, err := bpmn_compiler.New().ModuleCalls(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("ModuleCalls: %v", err)
	}
	if len(issues) > 0 {
		t.Fatalf("issues = %+v, want none", issues)
	}
	want := []domain.ModuleCall{
		{CallID: "CA_1", ProcessID: "Process_Lib", VersionNumber: 3},
		{CallID: "CA_2", ProcessID: "Process_Lib", VersionNumber: 0},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %+v, want %+v (the local Process_Local call is not a library call)", calls, want)
	}
}

func TestModuleCalls_ReportsARejectedElementInTheDocumentThatMakesTheCall(t *testing.T) {
	bpmnXML := mainWithCalls([]callSite{{ID: "CA_1", Lane: "Engineering", Calls: "Process_Lib"}})
	bpmnXML = strings.Replace(bpmnXML, "</bpmn:process>", `<bpmn:scriptTask id="Script_1" name="Run"/></bpmn:process>`, 1)

	calls, issues, err := bpmn_compiler.New().ModuleCalls(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("ModuleCalls: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want the one library call", calls)
	}
	var rejected bool
	for _, issue := range issues {
		rejected = rejected || (issue.Code == domain.BPMNErrRejectedElement && issue.NodeID == "Script_1")
	}
	if !rejected {
		t.Errorf("issues = %+v, want REJECTED_ELEMENT for Script_1: bundling the module drops the element, so the check has to read the document as authored", issues)
	}
}

func TestModuleCalls_ADocumentWithoutCallsIsLeftToValidate(t *testing.T) {
	bpmnXML := strings.Replace(libraryModule("Process_Lib"), "</bpmn:process>", `<bpmn:scriptTask id="Script_1"/></bpmn:process>`, 1)

	calls, issues, err := bpmn_compiler.New().ModuleCalls(context.Background(), bpmnXML)
	if err != nil || len(calls) != 0 || len(issues) != 0 {
		t.Errorf("ModuleCalls = %+v, %+v, %v; want nothing, since Validate already rejects this document", calls, issues, err)
	}
}

func TestModuleCalls_FlagsCallsThatCannotNameALibraryModule(t *testing.T) {
	base := mainWithCalls([]callSite{{ID: "CA_1", Lane: "Engineering", Calls: "Process_Lib"}})
	cases := map[string]string{
		"expression processId":     pinned(base, "Process_Lib", `attrs:processId="= &quot;Process_&quot; + kind"`),
		"version tag not a number": pinned(base, "Process_Lib", "v3"),
		"version tag missing":      pinned(base, "Process_Lib", `attrs:processId="Process_Lib" bindingType="versionTag"`),
		"deployment, not in file":  pinned(base, "Process_Lib", `attrs:processId="Process_Lib" bindingType="deployment"`),
	}
	for name, bpmnXML := range cases {
		t.Run(name, func(t *testing.T) {
			calls, issues, err := bpmn_compiler.New().ModuleCalls(context.Background(), bpmnXML)
			if err != nil {
				t.Fatalf("ModuleCalls: %v", err)
			}
			if len(calls) != 0 {
				t.Errorf("calls = %+v, want none resolvable", calls)
			}
			if got := findCode(issues, domain.BPMNErrUnresolvedCalledElement); got == nil || got.NodeID != "CA_1" {
				t.Errorf("issues = %+v, want UNRESOLVED_CALLED_ELEMENT on CA_1", issues)
			}
		})
	}
}

// TestModuleCalls_PinningAProcessThisFileDefines_IsRefused pins that a call
// pinning a library version of a process id the file also defines is refused
// on the call, rather than left to strand the local copy as a second root.
func TestModuleCalls_PinningAProcessThisFileDefines_IsRefused(t *testing.T) {
	bpmnXML := pinned(mainWithCalls([]callSite{{ID: "CA_1", Lane: "Engineering", Calls: "Process_Lib"}},
		moduleWithTask("Process_Lib", validCandidateUser)), "Process_Lib", "2")
	calls, issues, err := bpmn_compiler.New().ModuleCalls(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("ModuleCalls: %v", err)
	}
	if len(calls) != 0 {
		t.Errorf("calls = %+v, want none", calls)
	}
	if got := findCode(issues, domain.BPMNErrUnresolvedCalledElement); got == nil || got.NodeID != "CA_1" || !strings.Contains(got.Message, "this file") {
		t.Errorf("issues = %+v, want UNRESOLVED_CALLED_ELEMENT on CA_1 saying the file defines the process", issues)
	}
}

func TestBundleModules_TwoVersionsOfOneModuleBecomeTwoPlans(t *testing.T) {
	c := bpmn_compiler.New()
	main := pinned(mainWithCalls([]callSite{
		{ID: "CA_1", Lane: "Engineering", Calls: "Process_Lib"},
		{ID: "CA_2", Lane: "Ops", Calls: "Process_Lib"},
	}), "Process_Lib", "3")
	bundled, err := c.BundleModules(main, []domain.ResolvedModule{
		{ProcessID: "Process_Lib", VersionNumber: 3, PlanName: "Process_Lib@v3", BPMNXML: libraryModule("Process_Lib")},
		{ProcessID: "Process_Lib", VersionNumber: 0, PlanName: "Process_Lib@v4", BPMNXML: libraryModule("Process_Lib")},
	})
	if err != nil {
		t.Fatalf("BundleModules: %v", err)
	}

	collab := compileForPublish(t, bundled)
	planNamed(t, collab, "Process_Lib@v3")
	planNamed(t, collab, "Process_Lib@v4")
	calls := callSteps(planNamed(t, collab, collab.MainPlan))
	if calls[0].Plan != "Process_Lib@v3" || calls[1].Plan != "Process_Lib@v4" {
		t.Errorf("calls name %q and %q, want the pinned v3 and the latest v4", calls[0].Plan, calls[1].Plan)
	}
	if _, err := dsl.ExpandCalls(collab, domain.MaxPlanStages); err != nil {
		t.Errorf("the bundle does not expand: %v", err)
	}
}

func TestBundleModules_OneVersionReachedTwiceIsBundledOnce(t *testing.T) {
	main := mainWithCalls([]callSite{
		{ID: "CA_1", Lane: "Engineering", Calls: "Process_Lib"},
		{ID: "CA_2", Lane: "Ops", Calls: "Process_Lib"},
	})
	main = pinned(main, "Process_Lib", "4")
	bundled, err := bpmn_compiler.New().BundleModules(main, []domain.ResolvedModule{
		{ProcessID: "Process_Lib", VersionNumber: 4, PlanName: "Process_Lib@v4", BPMNXML: libraryModule("Process_Lib")},
		{ProcessID: "Process_Lib", VersionNumber: 0, PlanName: "Process_Lib@v4", BPMNXML: libraryModule("Process_Lib")},
	})
	if err != nil {
		t.Fatalf("BundleModules: %v", err)
	}
	if n := strings.Count(bundled, `id="Process_Lib@v4"`); n != 1 {
		t.Errorf("Process_Lib@v4 bundled %d times, want once", n)
	}
	compileForPublish(t, bundled)
}

func TestValidateModule_OpenLanesAllowedAndGlobalModulesNameNoUser(t *testing.T) {
	module := moduleDocument(lanedModule("Process_R", moduleLane{"Sender", "", "R_Send"}, moduleLane{"Receiver", "", "R_Receive"}))
	c := bpmn_compiler.New()

	errs, err := c.ValidateModule(context.Background(), module, "Process_R", false)
	if err != nil {
		t.Fatalf("ValidateModule: %v", err)
	}
	if blocking := blockingErrors(errs); len(blocking) > 0 {
		t.Errorf("a tenant module with open lanes is valid; got %+v", blocking)
	}

	errs, err = c.ValidateModule(context.Background(), module, "Process_R", true)
	if err != nil {
		t.Fatalf("ValidateModule: %v", err)
	}
	if got := findCode(errs, domain.BPMNErrInvalidCandidateUser); got == nil || !strings.Contains(got.Message, "global") {
		t.Errorf("errors = %+v, want INVALID_CANDIDATE_USER: a global module names no user", errs)
	}
}

// moduleCalling is a laneless module whose one step calls target, with the
// given zeebe:input target→source pairs.
func moduleCalling(id, target string, inputs ...[2]string) string {
	var io strings.Builder
	if len(inputs) > 0 {
		io.WriteString("<zeebe:ioMapping>")
		for _, in := range inputs {
			fmt.Fprintf(&io, "<zeebe:input source=\"%s\" target=%q/>", html.EscapeString(in[1]), in[0])
		}
		io.WriteString("</zeebe:ioMapping>")
	}
	return fmt.Sprintf(`  <bpmn:process id="%[1]s" isExecutable="true">
    <bpmn:startEvent id="%[1]s_Start"/>
    <bpmn:callActivity id="%[1]s_Call"><bpmn:extensionElements><zeebe:calledElement processId="%[2]s"/>%[3]s</bpmn:extensionElements></bpmn:callActivity>
    <bpmn:endEvent id="%[1]s_End"/>
    <bpmn:sequenceFlow id="%[1]s_F1" sourceRef="%[1]s_Start" targetRef="%[1]s_Call"/>
    <bpmn:sequenceFlow id="%[1]s_F2" sourceRef="%[1]s_Call" targetRef="%[1]s_End"/>
  </bpmn:process>`, id, target, io.String())
}

func TestBundleModules_ALocalCallKeepsItsProcessWhenAModuleCallsALibraryModuleOfTheSameID(t *testing.T) {
	main := mainWithCalls([]callSite{
		{ID: "CA_1", Lane: "Engineering", Calls: "Process_Lib"},
		{ID: "CA_L", Lane: "Ops", Calls: "Process_Check"},
	}, moduleWithTask("Process_Check", validCandidateUser))
	bundled, err := bpmn_compiler.New().BundleModules(main, []domain.ResolvedModule{
		{ProcessID: "Process_Lib", PlanName: "Process_Lib@v1", BPMNXML: moduleDocument(moduleCalling("Process_Lib", "Process_Check"))},
		{ProcessID: "Process_Check", PlanName: "Process_Check@v2", BPMNXML: libraryModule("Process_Check")},
	})
	if err != nil {
		t.Fatalf("BundleModules: %v", err)
	}

	collab := compileForPublish(t, bundled)
	calls := callSteps(planNamed(t, collab, collab.MainPlan))
	if calls[0].Plan != "Process_Lib@v1" || calls[1].Plan != "Process_Check" {
		t.Errorf("main calls name %q and %q, want Process_Lib@v1 and the local Process_Check", calls[0].Plan, calls[1].Plan)
	}
	if got := callSteps(planNamed(t, collab, "Process_Lib@v1"))[0].Plan; got != "Process_Check@v2" {
		t.Errorf("the module's call names %q, want the library Process_Check@v2", got)
	}
}

func TestValidateModule_GlobalModuleNamesNoUserThroughACallsAssignees(t *testing.T) {
	module := moduleDocument(
		moduleCalling("Process_G", "Process_Check@v1", [2]string{"Assignees", `{"Process_Check@v1_Task":"` + validCandidateUser + `"}`}),
		moduleWithTask("Process_Check@v1", ""),
	)
	errs, err := bpmn_compiler.New().ValidateModule(context.Background(), module, "Process_G", true)
	if err != nil {
		t.Fatalf("ValidateModule: %v", err)
	}
	if got := findCode(errs, domain.BPMNErrInvalidCandidateUser); got == nil || got.NodeID != "Process_G_Call" {
		t.Errorf("errors = %+v, want INVALID_CANDIDATE_USER on the call naming a user", errs)
	}
}

// TestValidateModule_GlobalModuleUserTwoSubProcessesDown_IsRefused pins why
// namedUsers looks one subProcess level deep: a deeper subProcess, where a
// user could hide from it, is itself refused.
func TestValidateModule_GlobalModuleUserTwoSubProcessesDown_IsRefused(t *testing.T) {
	module := moduleDocument(`  <bpmn:process id="Process_G" isExecutable="true">
    <bpmn:startEvent id="G_Start"/>
    <bpmn:subProcess id="G_Outer">
      <bpmn:startEvent id="GO_Start"/>
      <bpmn:subProcess id="G_Inner">
        <bpmn:startEvent id="GI_Start"/>
        <bpmn:userTask id="GI_Task"><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparator" candidateUsers="` + validCandidateUser + `"/></bpmn:extensionElements></bpmn:userTask>
        <bpmn:endEvent id="GI_End"/>
        <bpmn:sequenceFlow id="GI_F1" sourceRef="GI_Start" targetRef="GI_Task"/>
        <bpmn:sequenceFlow id="GI_F2" sourceRef="GI_Task" targetRef="GI_End"/>
      </bpmn:subProcess>
      <bpmn:endEvent id="GO_End"/>
      <bpmn:sequenceFlow id="GO_F1" sourceRef="GO_Start" targetRef="G_Inner"/>
      <bpmn:sequenceFlow id="GO_F2" sourceRef="G_Inner" targetRef="GO_End"/>
    </bpmn:subProcess>
    <bpmn:endEvent id="G_End"/>
    <bpmn:sequenceFlow id="G_F1" sourceRef="G_Start" targetRef="G_Outer"/>
    <bpmn:sequenceFlow id="G_F2" sourceRef="G_Outer" targetRef="G_End"/>
  </bpmn:process>`)
	errs, err := bpmn_compiler.New().ValidateModule(context.Background(), module, "Process_G", true)
	if err != nil {
		t.Fatalf("ValidateModule: %v", err)
	}
	if got := findCode(blockingErrors(errs), domain.BPMNErrNestedSubProcessNotSupported); got == nil {
		t.Errorf("errors = %+v, want the nested subProcess refused", errs)
	}
}

func TestProcessID_MissingNamespaceIsAValidationError(t *testing.T) {
	noZeebe := strings.Replace(libraryModule("Process_Lib"), ` xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"`, "", 1)
	_, err := bpmn_compiler.New().ProcessID(context.Background(), noZeebe)
	var vf *domain.ValidationFailedError
	if !errors.As(err, &vf) || vf.Errors[0].Code != domain.BPMNErrMissingNamespace {
		t.Errorf("err = %v, want a ValidationFailedError with MISSING_NAMESPACE, as Validate reports it", err)
	}
}
