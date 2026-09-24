package bpmn_compiler_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// callerBPMN is a laned main process whose one callActivity (CA_1) calls
// `calls`. Every other process in the document comes from procs.
func callerBPMN(mainID, calls string, procs ...string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_Caller" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="%[1]s" name="Main" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_mgmt" name="management"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="2200a5ae-2466-54f7-a1d8-243106e05705"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CA_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1"/>
    <bpmn:callActivity id="CA_1">
      <bpmn:extensionElements>
        <zeebe:calledElement processId="%[2]s"/>
        <zeebe:ioMapping><zeebe:input source="engineering" target="dept_id"/></zeebe:ioMapping>
      </bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="End_1"/>
    <bpmn:sequenceFlow id="Flow_s_ca" sourceRef="Start_1" targetRef="CA_1"/>
    <bpmn:sequenceFlow id="Flow_ca_e" sourceRef="CA_1" targetRef="End_1"/>
  </bpmn:process>
%[3]s
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="%[1]s">
      <bpmndi:BPMNShape id="Lane_mgmt_di" bpmnElement="Lane_mgmt"/>
      <bpmndi:BPMNShape id="Start_1_di" bpmnElement="Start_1"/>
      <bpmndi:BPMNShape id="CA_1_di" bpmnElement="CA_1"/>
      <bpmndi:BPMNShape id="End_1_di" bpmnElement="End_1"/>
      <bpmndi:BPMNEdge id="Flow_s_ca_di" bpmnElement="Flow_s_ca"/>
      <bpmndi:BPMNEdge id="Flow_ca_e_di" bpmnElement="Flow_ca_e"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`, mainID, calls, strings.Join(procs, "\n"))
}

// modulePassingTo is a laneless process whose only activity, <id>_CA, calls `calls`.
func modulePassingTo(id, calls string) string {
	return fmt.Sprintf(`  <bpmn:process id="%[1]s" isExecutable="true">
    <bpmn:startEvent id="%[1]s_Start"/>
    <bpmn:callActivity id="%[1]s_CA">
      <bpmn:extensionElements>
        <zeebe:calledElement processId="%[2]s"/>
        <zeebe:ioMapping><zeebe:input source="engineering" target="dept_id"/></zeebe:ioMapping>
      </bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="%[1]s_End"/>
    <bpmn:sequenceFlow id="%[1]s_F1" sourceRef="%[1]s_Start" targetRef="%[1]s_CA"/>
    <bpmn:sequenceFlow id="%[1]s_F2" sourceRef="%[1]s_CA" targetRef="%[1]s_End"/>
  </bpmn:process>`, id, calls)
}

// moduleCallingBoth is a laneless process that calls `first` from <id>_CA1 and
// then `second` from <id>_CA2.
func moduleCallingBoth(id, first, second string) string {
	return fmt.Sprintf(`  <bpmn:process id="%[1]s" isExecutable="true">
    <bpmn:startEvent id="%[1]s_Start"/>
    <bpmn:callActivity id="%[1]s_CA1">
      <bpmn:extensionElements>
        <zeebe:calledElement processId="%[2]s"/>
        <zeebe:ioMapping><zeebe:input source="engineering" target="dept_id"/></zeebe:ioMapping>
      </bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:callActivity id="%[1]s_CA2">
      <bpmn:extensionElements>
        <zeebe:calledElement processId="%[3]s"/>
        <zeebe:ioMapping><zeebe:input source="engineering" target="dept_id"/></zeebe:ioMapping>
      </bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="%[1]s_End"/>
    <bpmn:sequenceFlow id="%[1]s_F1" sourceRef="%[1]s_Start" targetRef="%[1]s_CA1"/>
    <bpmn:sequenceFlow id="%[1]s_F2" sourceRef="%[1]s_CA1" targetRef="%[1]s_CA2"/>
    <bpmn:sequenceFlow id="%[1]s_F3" sourceRef="%[1]s_CA2" targetRef="%[1]s_End"/>
  </bpmn:process>`, id, first, second)
}

func moduleCallingTwice(id, calls string) string { return moduleCallingBoth(id, calls, calls) }

// asCollaboration wraps a callerBPMN document's main process in a one-pool collaboration.
func asCollaboration(bpmnXML string) string {
	return strings.Replace(bpmnXML, "  <bpmn:process ",
		`  <bpmn:collaboration id="Collab_1"><bpmn:participant id="P_main" name="Main" processRef="Process_Main"/></bpmn:collaboration>
  <bpmn:process `, 1)
}

// moduleWithTask is a laneless process whose only activity is the userTask <id>_Task.
func moduleWithTask(id, candidateUsers string) string {
	return fmt.Sprintf(`  <bpmn:process id="%[1]s" isExecutable="true">
    <bpmn:startEvent id="%[1]s_Start"/>
    <bpmn:userTask id="%[1]s_Task">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="%[2]s"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="%[1]s_End"/>
    <bpmn:sequenceFlow id="%[1]s_F1" sourceRef="%[1]s_Start" targetRef="%[1]s_Task"/>
    <bpmn:sequenceFlow id="%[1]s_F2" sourceRef="%[1]s_Task" targetRef="%[1]s_End"/>
  </bpmn:process>`, id, candidateUsers)
}

// moduleDocument wraps processes as a standalone module_bpmn_xmls entry.
func moduleDocument(procs ...string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_Module" targetNamespace="http://bpmn.io/schema/bpmn">
` + strings.Join(procs, "\n") + `
</bpmn:definitions>`
}

const validCandidateUser = "550e8400-e29b-41d4-a716-446655440000"

func blockingErrors(errs []domain.BPMNValidationError) []domain.BPMNValidationError {
	var out []domain.BPMNValidationError
	for _, e := range errs {
		if e.Severity != domain.SeverityWarning {
			out = append(out, e)
		}
	}
	return out
}

func findCode(errs []domain.BPMNValidationError, code domain.BPMNErrorCode) *domain.BPMNValidationError {
	for i := range errs {
		if errs[i].Code == code {
			return &errs[i]
		}
	}
	return nil
}

func assertCompileRefusedWith(t *testing.T, bpmnXML string, code domain.BPMNErrorCode) {
	t.Helper()
	_, err := bpmn_compiler.New().CompileForPublish(context.Background(), bpmnXML)
	var vf *domain.ValidationFailedError
	if !errors.As(err, &vf) {
		t.Fatalf("CompileForPublish err = %v, want a ValidationFailedError carrying %s", err, code)
	}
	if !hasCode(vf.Errors, code) {
		t.Errorf("CompileForPublish errors = %v, want %s", errCodes(vf.Errors), code)
	}
}

func TestValidate_AcyclicModuleChain_IsValid(t *testing.T) {
	bpmnXML := callerBPMN("Process_Main", "Process_B",
		modulePassingTo("Process_B", "Process_C"),
		moduleWithTask("Process_C", validCandidateUser))

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if blocking := blockingErrors(errs); len(blocking) > 0 {
		t.Fatalf("fixture must be valid apart from what each test changes; got %+v", blocking)
	}
	if _, err := bpmn_compiler.New().CompileForPublish(context.Background(), bpmnXML); err != nil {
		t.Fatalf("compile: %v", err)
	}
}

func TestValidate_CalledProcessBody_IsValidated(t *testing.T) {
	bpmnXML := callerBPMN("Process_Main", "Process_B",
		modulePassingTo("Process_B", "Process_C"),
		moduleWithTask("Process_C", ""))

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	got := findCode(errs, domain.BPMNErrCandidateUsersEmpty)
	if got == nil {
		t.Fatalf("errors = %v, want CANDIDATE_USERS_EMPTY from the called process's task", errCodes(errs))
	}
	if got.NodeID != "Process_C_Task" {
		t.Errorf("CANDIDATE_USERS_EMPTY node = %q, want Process_C_Task", got.NodeID)
	}
	assertCompileRefusedWith(t, bpmnXML, domain.BPMNErrCandidateUsersEmpty)
}

func TestValidate_CalledProcessReachedTwice_IsReportedOnce(t *testing.T) {
	bpmnXML := callerBPMN("Process_Main", "Process_B",
		moduleCallingTwice("Process_B", "Process_C"),
		moduleWithTask("Process_C", ""))

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if n := countCode(errs, domain.BPMNErrCandidateUsersEmpty); n != 1 {
		t.Errorf("CANDIDATE_USERS_EMPTY reported %d times, want 1; errors = %v", n, errCodes(errs))
	}
}

func TestValidate_CalledElementCycle_IsRejected(t *testing.T) {
	cases := []struct {
		name     string
		bpmnXML  string
		closedAt string
		cycle    string
	}{
		{
			name:     "module calls itself",
			bpmnXML:  callerBPMN("Process_Main", "Process_A", modulePassingTo("Process_A", "Process_A")),
			closedAt: "Process_A_CA",
			cycle:    "Process_A → Process_A",
		},
		{
			name: "two modules call each other",
			bpmnXML: callerBPMN("Process_Main", "Process_B",
				modulePassingTo("Process_B", "Process_C"),
				modulePassingTo("Process_C", "Process_B")),
			closedAt: "Process_C_CA",
			cycle:    "Process_B → Process_C → Process_B",
		},
		{
			name:     "root calls itself",
			bpmnXML:  callerBPMN("Process_Main", "Process_Main"),
			closedAt: "CA_1",
			cycle:    "Process_Main → Process_Main",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs, err := bpmn_compiler.New().Validate(context.Background(), tc.bpmnXML)
			if err != nil {
				t.Fatalf("validate: %v", err)
			}
			switch got := findCode(errs, domain.BPMNErrCalledElementCycle); {
			case got == nil:
				t.Errorf("errors = %v, want CALLED_ELEMENT_CYCLE", errCodes(errs))
			case got.NodeID != tc.closedAt:
				t.Errorf("CALLED_ELEMENT_CYCLE node = %q, want %q", got.NodeID, tc.closedAt)
			case !strings.HasSuffix(got.Message, "call cycle "+tc.cycle):
				t.Errorf("CALLED_ELEMENT_CYCLE message = %q, want it to name only the cycle %q", got.Message, tc.cycle)
			}
			assertCompileRefusedWith(t, tc.bpmnXML, domain.BPMNErrCalledElementCycle)
		})
	}
}

func TestValidate_SharedCalledProcess_IsNotACycle(t *testing.T) {
	cases := []struct {
		name    string
		bpmnXML string
	}{
		{
			name: "diamond",
			bpmnXML: callerBPMN("Process_Main", "Process_A",
				moduleCallingBoth("Process_A", "Process_B", "Process_C"),
				modulePassingTo("Process_B", "Process_D"),
				modulePassingTo("Process_C", "Process_D"),
				moduleWithTask("Process_D", validCandidateUser)),
		},
		{
			name: "two call sites",
			bpmnXML: callerBPMN("Process_Main", "Process_B",
				moduleCallingTwice("Process_B", "Process_C"),
				moduleWithTask("Process_C", validCandidateUser)),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs, err := bpmn_compiler.New().Validate(context.Background(), tc.bpmnXML)
			if err != nil {
				t.Fatalf("validate: %v", err)
			}
			if blocking := blockingErrors(errs); len(blocking) > 0 {
				t.Errorf("a process reached twice is not a cycle; got %+v", blocking)
			}
			if _, err := bpmn_compiler.New().CompileForPublish(context.Background(), tc.bpmnXML); err != nil {
				t.Errorf("compile: %v", err)
			}
		})
	}
}

func TestCompileCollaboration_CalledElementCycle_IsRejected(t *testing.T) {
	bpmnXML := asCollaboration(callerBPMN("Process_Main", "Process_A", modulePassingTo("Process_A", "Process_A")))
	if !strings.Contains(bpmnXML, "<bpmn:collaboration") {
		t.Fatal("fixture is not a collaboration")
	}

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if n := countCode(errs, domain.BPMNErrCalledElementCycle); n != 1 {
		t.Errorf("CALLED_ELEMENT_CYCLE reported %d times, want 1; errors = %v", n, errCodes(errs))
	}

	_, err = bpmn_compiler.New().CompileCollaboration(context.Background(), bpmnXML)
	var vf *domain.ValidationFailedError
	if !errors.As(err, &vf) || !hasCode(vf.Errors, domain.BPMNErrCalledElementCycle) {
		t.Errorf("CompileCollaboration err = %v, want a ValidationFailedError carrying CALLED_ELEMENT_CYCLE", err)
	}
}

func TestValidate_CalledProcessWithoutStart_IsReportedAtTheCallSite(t *testing.T) {
	module := moduleWithTask("Process_C", validCandidateUser)
	module = strings.Replace(module, `    <bpmn:startEvent id="Process_C_Start"/>
`, "", 1)
	module = strings.Replace(module, `    <bpmn:sequenceFlow id="Process_C_F1" sourceRef="Process_C_Start" targetRef="Process_C_Task"/>
`, "", 1)
	if strings.Contains(module, "Process_C_Start") {
		t.Fatal("fixture still carries the start event this test removes")
	}

	errs, err := bpmn_compiler.New().Validate(context.Background(), callerBPMN("Process_Main", "Process_C", module))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := findCode(errs, domain.BPMNErrNoStartEvent); got == nil || got.NodeID != "CA_1" {
		t.Errorf("errors = %+v, want NO_START_EVENT on the call site CA_1", errs)
	}
	for _, e := range errs {
		if e.NodeID == "" {
			t.Errorf("%s %q names no node; the user cannot tell which process it is about", e.Code, e.Message)
		}
	}
}

func TestValidate_CalledProcessLevelError_NamesTheProcess(t *testing.T) {
	module := moduleWithTask("Process_C", validCandidateUser)
	module = strings.Replace(module, `    <bpmn:endEvent id="Process_C_End"/>
`, "", 1)
	module = strings.Replace(module, `    <bpmn:sequenceFlow id="Process_C_F2" sourceRef="Process_C_Task" targetRef="Process_C_End"/>
`, "", 1)
	if strings.Contains(module, "Process_C_End") {
		t.Fatal("fixture still carries the end event this test removes")
	}

	errs, err := bpmn_compiler.New().Validate(context.Background(), callerBPMN("Process_Main", "Process_C", module))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := findCode(errs, domain.BPMNErrNoEndEvent); got == nil || got.NodeID != "Process_C" {
		t.Errorf("errors = %+v, want NO_END_EVENT on the called process Process_C", errs)
	}
}

func TestValidate_UnresolvedCalledElement_ReportedOnce(t *testing.T) {
	errs, err := bpmn_compiler.New().Validate(context.Background(), callActivityUnknownRefBPMN)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if n := countCode(errs, domain.BPMNErrUnresolvedCalledElement); n != 1 {
		t.Errorf("UNRESOLVED_CALLED_ELEMENT reported %d times, want 1", n)
	}
}

func TestValidate_DuplicateProcessID_IsRejected(t *testing.T) {
	cases := []struct {
		name    string
		modules []string
	}{
		{
			name: "two modules share an id",
			modules: []string{
				moduleDocument(moduleWithTask("Process_A", validCandidateUser)),
				moduleDocument(moduleWithTask("Process_A", "")),
			},
		},
		{
			name: "a module reuses the main process id",
			modules: []string{
				moduleDocument(moduleWithTask("Process_A", validCandidateUser)),
				moduleDocument(moduleWithTask("Process_Main", validCandidateUser)),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := bpmn_compiler.New()
			bundled, err := c.Bundle(callerBPMN("Process_Main", "Process_A"), tc.modules)
			if err != nil {
				t.Fatalf("bundle: %v", err)
			}
			errs, err := c.Validate(context.Background(), bundled)
			if err != nil {
				t.Fatalf("validate: %v", err)
			}
			if !hasCode(errs, domain.BPMNErrDuplicateProcessID) {
				t.Errorf("errors = %v, want DUPLICATE_PROCESS_ID", errCodes(errs))
			}
			assertCompileRefusedWith(t, bundled, domain.BPMNErrDuplicateProcessID)
		})
	}
}

func countCode(errs []domain.BPMNValidationError, code domain.BPMNErrorCode) int {
	n := 0
	for _, e := range errs {
		if e.Code == code {
			n++
		}
	}
	return n
}
