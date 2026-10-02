package bpmn_compiler_test

import (
	"context"
	"errors"
	"fmt"
	"html"
	"reflect"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const (
	engDeptID   = "2200a5ae-2466-54f7-a1d8-243106e05705"
	opsDeptID   = "92fd92cf-4b2c-58dc-bf98-f3d0af729876"
	legalDeptID = "f14abc9f-befa-54ff-b57f-c15ab3090062"
	otherUser   = "018f2d3c-0000-7000-8000-0000000000b2"
)

// callSite is one callActivity of mainWithCalls. Inputs are zeebe:input
// target→source pairs; Boundaries are boundary kinds: message, timer or error.
type callSite struct {
	ID, Lane, Calls string
	Inputs          [][2]string
	Outputs         [][2]string
	Props           [][2]string
	Boundaries      []string
}

// mainWithCalls is a main process with two lanes, Engineering and Ops, that
// runs its call sites in sequence and appends procs as further processes.
func mainWithCalls(calls []callSite, procs ...string) string {
	var nodes, flows, laneRefs, shapes strings.Builder
	refs := map[string][]string{"Engineering": {"Start_1", "End_1"}, "Ops": nil}
	prev := "Start_1"
	for i, c := range calls {
		refs[c.Lane] = append(refs[c.Lane], c.ID)
		fmt.Fprintf(&nodes, "    <bpmn:callActivity id=%q name=\"Call %d\"><bpmn:extensionElements><zeebe:calledElement processId=%q/>", c.ID, i+1, c.Calls)
		if len(c.Inputs)+len(c.Outputs) > 0 {
			nodes.WriteString("<zeebe:ioMapping>")
			for _, in := range c.Inputs {
				fmt.Fprintf(&nodes, "<zeebe:input source=\"%s\" target=%q/>", html.EscapeString(in[1]), in[0])
			}
			for _, out := range c.Outputs {
				fmt.Fprintf(&nodes, "<zeebe:output source=\"%s\" target=%q/>", html.EscapeString(out[1]), out[0])
			}
			nodes.WriteString("</zeebe:ioMapping>")
		}
		if len(c.Props) > 0 {
			nodes.WriteString("<zeebe:properties>")
			for _, pr := range c.Props {
				fmt.Fprintf(&nodes, "<zeebe:property name=%q value=%q/>", pr[0], pr[1])
			}
			nodes.WriteString("</zeebe:properties>")
		}
		nodes.WriteString("</bpmn:extensionElements></bpmn:callActivity>\n")
		fmt.Fprintf(&flows, "    <bpmn:sequenceFlow id=\"Flow_%s\" sourceRef=%q targetRef=%q/>\n", c.ID, prev, c.ID)
		shapes.WriteString(edge("Flow_" + c.ID))
		for _, kind := range c.Boundaries {
			be := c.ID + "_" + kind
			refs[c.Lane] = append(refs[c.Lane], be, be+"_End")
			fmt.Fprintf(&nodes, "    <bpmn:boundaryEvent id=%q attachedToRef=%q>%s</bpmn:boundaryEvent>\n", be, c.ID, boundaryDefinition(kind))
			fmt.Fprintf(&nodes, "    <bpmn:endEvent id=\"%s_End\"/>\n", be)
			fmt.Fprintf(&flows, "    <bpmn:sequenceFlow id=\"Flow_%s\" sourceRef=%q targetRef=\"%s_End\"/>\n", be, be, be)
			shapes.WriteString(shape(be) + shape(be+"_End") + edge("Flow_"+be))
		}
		prev = c.ID
	}
	fmt.Fprintf(&flows, "    <bpmn:sequenceFlow id=\"Flow_end\" sourceRef=%q targetRef=\"End_1\"/>\n", prev)
	shapes.WriteString(edge("Flow_end"))
	for _, lane := range []struct{ id, name, dept string }{{"Lane_eng", "Engineering", engDeptID}, {"Lane_ops", "Ops", opsDeptID}} {
		fmt.Fprintf(&laneRefs, "      <bpmn:lane id=%q name=%q><bpmn:extensionElements><zeebe:properties><zeebe:property name=\"dept_id\" value=%q/></zeebe:properties></bpmn:extensionElements>\n", lane.id, lane.name, lane.dept)
		for _, r := range refs[lane.name] {
			fmt.Fprintf(&laneRefs, "        <bpmn:flowNodeRef>%s</bpmn:flowNodeRef>\n", r)
		}
		laneRefs.WriteString("      </bpmn:lane>\n")
		shapes.WriteString(shape(lane.id))
	}
	shapes.WriteString(shape("Start_1") + shape("End_1"))
	for _, c := range calls {
		shapes.WriteString(shape(c.ID))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_Calls" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="Msg_withdraw" name="withdraw"/>
  <bpmn:error id="Err_reject" errorCode="E_REJECT"/>
  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
%s    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1"/>
%s    <bpmn:endEvent id="End_1"/>
%s  </bpmn:process>
%s
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Process_Main">
%s    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`, laneRefs.String(), nodes.String(), flows.String(), strings.Join(procs, "\n"), shapes.String())
}

func shape(id string) string {
	return fmt.Sprintf("      <bpmndi:BPMNShape id=\"%s_di\" bpmnElement=%q/>\n", id, id)
}

func edge(id string) string {
	return fmt.Sprintf("      <bpmndi:BPMNEdge id=\"%s_di\" bpmnElement=%q/>\n", id, id)
}

func boundaryDefinition(kind string) string {
	switch kind {
	case "message":
		return `<bpmn:messageEventDefinition id="MED" messageRef="Msg_withdraw"/>`
	case "timer":
		return `<bpmn:timerEventDefinition id="TED"><bpmn:timeDuration>P5D</bpmn:timeDuration></bpmn:timerEventDefinition>`
	default:
		return `<bpmn:errorEventDefinition id="EED" errorRef="Err_reject"/>`
	}
}

// moduleLane is one lane of lanedModule, holding one userTask.
type moduleLane struct{ Name, DeptID, TaskID string }

// lanedModule is a module whose lanes each hold one task, run in lane order.
// A lane with an empty DeptID is an open slot the call site binds.
func lanedModule(id string, lanes ...moduleLane) string {
	var laneXML, nodes, flows strings.Builder
	prev := id + "_Start"
	for i, l := range lanes {
		fmt.Fprintf(&laneXML, "      <bpmn:lane id=\"%s_Lane%d\" name=%q>", id, i, l.Name)
		if l.DeptID != "" {
			fmt.Fprintf(&laneXML, "<bpmn:extensionElements><zeebe:properties><zeebe:property name=\"dept_id\" value=%q/></zeebe:properties></bpmn:extensionElements>", l.DeptID)
		}
		if i == 0 {
			fmt.Fprintf(&laneXML, "<bpmn:flowNodeRef>%s_Start</bpmn:flowNodeRef>", id)
		}
		fmt.Fprintf(&laneXML, "<bpmn:flowNodeRef>%s</bpmn:flowNodeRef>", l.TaskID)
		if i == len(lanes)-1 {
			fmt.Fprintf(&laneXML, "<bpmn:flowNodeRef>%s_End</bpmn:flowNodeRef>", id)
		}
		laneXML.WriteString("</bpmn:lane>\n")
		fmt.Fprintf(&nodes, `    <bpmn:userTask id=%q><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers=%q/></bpmn:extensionElements></bpmn:userTask>
`, l.TaskID, validCandidateUser)
		fmt.Fprintf(&flows, "    <bpmn:sequenceFlow id=\"%s_F%d\" sourceRef=%q targetRef=%q/>\n", id, i, prev, l.TaskID)
		prev = l.TaskID
	}
	fmt.Fprintf(&flows, "    <bpmn:sequenceFlow id=\"%s_FEnd\" sourceRef=%q targetRef=\"%s_End\"/>\n", id, prev, id)
	return fmt.Sprintf(`  <bpmn:process id=%q isExecutable="true">
    <bpmn:laneSet id="%s_LaneSet">
%s    </bpmn:laneSet>
    <bpmn:startEvent id="%s_Start"/>
%s    <bpmn:endEvent id="%s_End"/>
%s  </bpmn:process>`, id, id, laneXML.String(), id, nodes.String(), id, flows.String())
}

func compileForPublish(t *testing.T, bpmnXML string) *dsl.CompiledCollaboration {
	t.Helper()
	collab, err := bpmn_compiler.New().CompileForPublish(context.Background(), bpmnXML)
	if err != nil {
		var vf *domain.ValidationFailedError
		if errors.As(err, &vf) {
			t.Fatalf("CompileForPublish refused it: %+v", vf.Errors)
		}
		t.Fatalf("CompileForPublish: %v", err)
	}
	return collab
}

func validateBlocking(t *testing.T, bpmnXML string) []domain.BPMNValidationError {
	t.Helper()
	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	return blockingErrors(errs)
}

func planNamed(t *testing.T, c *dsl.CompiledCollaboration, name string) *dsl.CompiledPlan {
	t.Helper()
	for _, p := range c.Plans {
		if p.Name == name {
			return p
		}
	}
	var names []string
	for _, p := range c.Plans {
		names = append(names, p.Name)
	}
	t.Fatalf("plan %q not compiled; plans = %v", name, names)
	return nil
}

func callSteps(p *dsl.CompiledPlan) []*dsl.CallPlanStep {
	var out []*dsl.CallPlanStep
	for _, s := range p.Execution.Steps {
		if s.CallPlan != nil {
			out = append(out, s.CallPlan)
		}
	}
	return out
}

func expandedMain(t *testing.T, c *dsl.CompiledCollaboration) *dsl.CompiledPlan {
	t.Helper()
	out, err := dsl.ExpandCalls(c, 0)
	if err != nil {
		t.Fatalf("ExpandCalls: %v", err)
	}
	return planNamed(t, out, out.MainPlan)
}

func departmentNamed(t *testing.T, p *dsl.CompiledPlan, id string) dsl.DepartmentDef {
	t.Helper()
	for _, d := range p.Departments {
		if d.ID == id {
			return d
		}
	}
	var ids []string
	for _, d := range p.Departments {
		ids = append(ids, d.ID)
	}
	t.Fatalf("department %q not in plan %q; departments = %v", id, p.Name, ids)
	return dsl.DepartmentDef{}
}

func TestCompileForPublish_CalledModuleIsCompiledOnceAndReferencedByEachCall(t *testing.T) {
	collab := compileForPublish(t, mainWithCalls([]callSite{
		{ID: "CA_1", Lane: "Engineering", Calls: "Process_M"},
		{ID: "CA_2", Lane: "Ops", Calls: "Process_M"},
	}, moduleWithTask("Process_M", validCandidateUser)))

	if len(collab.Plans) != 2 {
		t.Fatalf("plans = %d, want the main plan and one plan for the module", len(collab.Plans))
	}
	module := planNamed(t, collab, "Process_M")
	if len(module.Departments) != 1 || module.Departments[0].ID != "Process_M" || module.Departments[0].Stages[0].NodeID != "Process_M_Task" {
		t.Errorf("module plan departments = %+v, want one department Process_M holding Process_M_Task", module.Departments)
	}
	main := planNamed(t, collab, collab.MainPlan)
	calls := callSteps(main)
	if len(calls) != 2 {
		t.Fatalf("main plan call steps = %d, want 2", len(calls))
	}
	want := []dsl.CallPlanStep{
		{NodeID: "CA_1", Name: "Call 1", Plan: "Process_M", Departments: map[string]string{"Process_M": "Engineering"}},
		{NodeID: "CA_2", Name: "Call 2", Plan: "Process_M", Departments: map[string]string{"Process_M": "Ops"}},
	}
	for i := range want {
		if !reflect.DeepEqual(*calls[i], want[i]) {
			t.Errorf("call %d = %+v, want %+v", i, *calls[i], want[i])
		}
	}
	for _, d := range main.Departments {
		for _, s := range d.Stages {
			if s.NodeID == "Process_M_Task" {
				t.Errorf("the module's task was inlined into the main plan's %q department", d.ID)
			}
		}
	}
}

func TestCompileForPublish_EachCallRunsTheModuleInTheCallersDepartment(t *testing.T) {
	main := expandedMain(t, compileForPublish(t, mainWithCalls([]callSite{
		{ID: "CA_1", Lane: "Engineering", Calls: "Process_M"},
		{ID: "CA_2", Lane: "Ops", Calls: "Process_M"},
	}, moduleWithTask("Process_M", validCandidateUser))))

	for id, iam := range map[string]string{"CA_1::Process_M": engDeptID, "CA_2::Process_M": opsDeptID} {
		d := departmentNamed(t, main, id)
		if d.IAMDepartmentID != iam || len(d.Stages) != 1 || d.Stages[0].NodeID != "Process_M_Task" {
			t.Errorf("%s = {iam %q, stages %+v}, want iam %s with Process_M_Task", id, d.IAMDepartmentID, d.Stages, iam)
		}
	}
}

func TestCompileForPublish_ModuleLanesMappedByDepts(t *testing.T) {
	module := lanedModule("Process_R", moduleLane{"Sender", "", "R_Send"}, moduleLane{"Receiver", "", "R_Receive"})
	for _, depts := range []string{`{"sender":"Engineering","Receiver":"Ops"}`, `={"Sender":"Engineering","receiver":"Ops"}`} {
		t.Run(depts, func(t *testing.T) {
			collab := compileForPublish(t, mainWithCalls([]callSite{
				{ID: "CA_1", Lane: "Engineering", Calls: "Process_R", Inputs: [][2]string{{"Depts", depts}}},
			}, module))
			call := callSteps(planNamed(t, collab, collab.MainPlan))[0]
			if want := map[string]string{"Sender": "Engineering", "Receiver": "Ops"}; !reflect.DeepEqual(call.Departments, want) {
				t.Errorf("departments = %v, want %v", call.Departments, want)
			}
			if got := departmentNamed(t, expandedMain(t, collab), "CA_1::Receiver").IAMDepartmentID; got != opsDeptID {
				t.Errorf("Receiver runs in iam %q, want Ops %s", got, opsDeptID)
			}
		})
	}
}

func TestCompileForPublish_OneOpenLaneTakesTheCallsLane_FixedLaneKeepsItsDepartment(t *testing.T) {
	collab := compileForPublish(t, mainWithCalls([]callSite{
		{ID: "CA_1", Lane: "Ops", Calls: "Process_R"},
	}, lanedModule("Process_R", moduleLane{"Sender", "", "R_Send"}, moduleLane{"Legal", legalDeptID, "R_Check"})))

	if got := callSteps(planNamed(t, collab, collab.MainPlan))[0].Departments; !reflect.DeepEqual(got, map[string]string{"Sender": "Ops"}) {
		t.Errorf("departments = %v, want only the open lane bound to the call's lane Ops", got)
	}
	main := expandedMain(t, collab)
	if got := departmentNamed(t, main, "CA_1::Legal").IAMDepartmentID; got != legalDeptID {
		t.Errorf("the fixed Legal lane runs in iam %q, want its own %s", got, legalDeptID)
	}
}

func TestValidate_ModuleLaneBindingErrors(t *testing.T) {
	twoOpen := lanedModule("Process_R", moduleLane{"Sender", "", "R_Send"}, moduleLane{"Receiver", "", "R_Receive"})
	oneFixed := lanedModule("Process_R", moduleLane{"Sender", "", "R_Send"}, moduleLane{"Legal", legalDeptID, "R_Check"})
	cases := []struct {
		name     string
		module   string
		inputs   [][2]string
		code     domain.BPMNErrorCode
		mentions []string
	}{
		{"remaps a lane fixed to its own department", oneFixed, [][2]string{{"Depts", `{"Legal":"Engineering"}`}}, domain.BPMNErrInvalidZeebeProperty, []string{"Legal"}},
		{"maps a lane that holds no work", workFreeLaneModule(), [][2]string{{"Depts", `{"Starter":"Ops"}`}}, domain.BPMNErrInvalidZeebeProperty, []string{"Starter"}},
		{"maps one lane twice, differing in case", twoOpen, [][2]string{{"Depts", `{"Sender":"Ops","sender":"Engineering","Receiver":"Ops"}`}}, domain.BPMNErrInvalidZeebeProperty, []string{"Sender"}},
		{"two open lanes, nothing mapped", "", nil, domain.BPMNErrMissingDeptInputForModule, []string{"Sender", "Receiver"}},
		{"one of two open lanes mapped", "", [][2]string{{"Depts", `{"Sender":"Ops"}`}}, domain.BPMNErrMissingDeptInputForModule, []string{"Receiver"}},
		{"mapped to a lane the caller lacks", "", [][2]string{{"Depts", `{"Sender":"Finance","Receiver":"Ops"}`}}, domain.BPMNErrInvalidZeebeProperty, []string{"Finance"}},
		{"maps a lane the module lacks", "", [][2]string{{"Depts", `{"Sender":"Ops","Receiver":"Ops","Approver":"Ops"}`}}, domain.BPMNErrInvalidZeebeProperty, []string{"Approver"}},
		{"malformed Depts", "", [][2]string{{"Depts", `{"Sender":`}}, domain.BPMNErrInvalidZeebeProperty, []string{"Depts"}},
		{"retired dept_id input", "", [][2]string{{"dept_id", "Ops"}, {"Depts", `{"Sender":"Ops","Receiver":"Ops"}`}}, domain.BPMNErrInvalidZeebeProperty, []string{"dept_id"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			module := tc.module
			if module == "" {
				module = twoOpen
			}
			bpmnXML := mainWithCalls([]callSite{{ID: "CA_1", Lane: "Engineering", Calls: "Process_R", Inputs: tc.inputs}}, module)
			got := findCode(validateBlocking(t, bpmnXML), tc.code)
			if got == nil {
				t.Fatalf("no blocking %s; errors = %+v", tc.code, validateBlocking(t, bpmnXML))
			}
			if got.NodeID != "CA_1" {
				t.Errorf("%s names node %q, want the call CA_1", tc.code, got.NodeID)
			}
			for _, m := range tc.mentions {
				if !strings.Contains(got.Message, m) {
					t.Errorf("message %q does not mention %q", got.Message, m)
				}
			}
			assertCompileRefusedWith(t, bpmnXML, tc.code)
		})
	}
}

func TestCompileForPublish_CallSiteAssignees(t *testing.T) {
	collab := compileForPublish(t, mainWithCalls([]callSite{
		{ID: "CA_1", Lane: "Engineering", Calls: "Process_M", Inputs: [][2]string{{"Assignees", `{"Process_M_Task":"` + otherUser + `"}`}}},
	}, moduleWithTask("Process_M", validCandidateUser)))
	call := callSteps(planNamed(t, collab, collab.MainPlan))[0]
	if want := map[string]string{"Process_M_Task": otherUser}; !reflect.DeepEqual(call.Assignees, want) {
		t.Errorf("assignees = %v, want %v", call.Assignees, want)
	}
	for _, s := range planNamed(t, collab, collab.MainPlan).Execution.Steps {
		if s.CallPlan != nil && s.IOMapping != nil {
			t.Errorf("Assignees leaked into the call's runtime IOMapping: %+v", s.IOMapping)
		}
	}

	for name, assignees := range map[string]string{
		"unknown task":     `{"Nope":"` + otherUser + `"}`,
		"user is not uuid": `{"Process_M_Task":"someone"}`,
		"user is uuid v4":  `{"Process_M_Task":"9b2e4f1a-3c5d-4e6f-8a7b-1c2d3e4f5a6b"}`,
		"malformed":        `{"Process_M_Task":`,
	} {
		t.Run(name, func(t *testing.T) {
			bpmnXML := mainWithCalls([]callSite{
				{ID: "CA_1", Lane: "Engineering", Calls: "Process_M", Inputs: [][2]string{{"Assignees", assignees}}},
			}, moduleWithTask("Process_M", validCandidateUser))
			if got := findCode(validateBlocking(t, bpmnXML), domain.BPMNErrInvalidZeebeProperty); got == nil || got.NodeID != "CA_1" {
				t.Errorf("errors = %+v, want INVALID_ZEEBE_PROPERTY on CA_1", validateBlocking(t, bpmnXML))
			}
		})
	}
}

// TestCompileForPublish_CallSiteAssigneesReachANestedModuleByPath pins that a
// workflow names a user for a task two modules down by the path of calls to
// it, and that a path leading nowhere is refused on the call.
func TestCompileForPublish_CallSiteAssigneesReachANestedModuleByPath(t *testing.T) {
	withAssignees := func(assignees string) string {
		return mainWithCalls([]callSite{
			{ID: "CA_1", Lane: "Engineering", Calls: "Process_Lib", Inputs: [][2]string{{"Assignees", assignees}}},
		}, moduleCalling("Process_Lib", "Process_Check"), moduleWithTask("Process_Check", ""))
	}
	bpmnXML := withAssignees(`{"Process_Lib_Call::Process_Check_Task":"` + otherUser + `"}`)
	if errs := validateBlocking(t, bpmnXML); len(errs) > 0 {
		t.Fatalf("a path through the module's call is valid; got %+v", errs)
	}
	got := departmentNamed(t, expandedMain(t, compileForPublish(t, bpmnXML)), "CA_1::Process_Lib_Call::Process_Check")
	if !reflect.DeepEqual(got.Stages[0].DefaultAssignees, []string{otherUser}) {
		t.Errorf("nested task defaults = %v, want [%s]", got.Stages[0].DefaultAssignees, otherUser)
	}

	for key, mentions := range map[string]string{
		"Process_Lib_Call::Nope":        "Nope",
		"Nope_Call::Process_Check_Task": "Nope_Call",
	} {
		t.Run(key, func(t *testing.T) {
			got := findCode(validateBlocking(t, withAssignees(`{"`+key+`":"`+otherUser+`"}`)), domain.BPMNErrInvalidZeebeProperty)
			if got == nil || got.NodeID != "CA_1" || !strings.Contains(got.Message, mentions) {
				t.Errorf("error = %+v, want INVALID_ZEEBE_PROPERTY on CA_1 naming %q", got, mentions)
			}
		})
	}
}

func TestCompileForPublish_CallBoundaries(t *testing.T) {
	bpmnXML := mainWithCalls([]callSite{
		{ID: "CA_1", Lane: "Engineering", Calls: "Process_M", Boundaries: []string{"message", "timer", "error"}},
	}, moduleWithTask("Process_M", validCandidateUser))
	if errs := validateBlocking(t, bpmnXML); len(errs) > 0 {
		t.Fatalf("message, timer and error boundaries on a call are allowed; got %+v", errs)
	}
	call := callSteps(planNamed(t, compileForPublish(t, bpmnXML), "Main"))[0]
	if len(call.MessagePaths) != 1 || call.MessagePaths[0].MessageName != "withdraw" || !call.MessagePaths[0].Interrupting {
		t.Errorf("message paths = %+v", call.MessagePaths)
	}
	if len(call.TimerPaths) != 1 || call.TimerPaths[0].Duration != "P5D" {
		t.Errorf("timer paths = %+v", call.TimerPaths)
	}
	if len(call.ErrorPaths) != 1 || call.ErrorPaths[0].ErrorCode != "E_REJECT" {
		t.Errorf("error paths = %+v", call.ErrorPaths)
	}
}

func TestCompileForPublish_ModuleCallingAModule(t *testing.T) {
	collab := compileForPublish(t, mainWithCalls([]callSite{
		{ID: "CA_1", Lane: "Ops", Calls: "Process_A"},
	}, modulePassingTo("Process_A", "Process_C"), moduleWithTask("Process_C", validCandidateUser)))

	outer := planNamed(t, collab, "Process_A")
	if got := callSteps(outer); len(got) != 1 || got[0].Plan != "Process_C" || !reflect.DeepEqual(got[0].Departments, map[string]string{"Process_C": "Process_A"}) {
		t.Errorf("Process_A's call = %+v, want Process_C bound to Process_A's own department", got)
	}
	main := expandedMain(t, collab)
	if got := departmentNamed(t, main, "CA_1::Process_A_CA::Process_C").IAMDepartmentID; got != opsDeptID {
		t.Errorf("the nested module runs in iam %q, want the outer call's Ops %s", got, opsDeptID)
	}
}

// TestPublish_EveryHumanTaskNeedsADefaultUser pins that a module may name no
// user, but once a workflow calls it each of its tasks needs one: the call's
// Assignees or the module's own. The rule is judged on the expanded plan, so
// it is a warning on the documents alone and an error once publishable.
func TestPublish_EveryHumanTaskNeedsADefaultUser(t *testing.T) {
	c := bpmn_compiler.New()
	unassigned := mainWithCalls([]callSite{{ID: "CA_1", Lane: "Engineering", Calls: "Process_M"}}, moduleWithTask("Process_M", ""))

	errs, err := c.Validate(context.Background(), unassigned)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := findCode(errs, domain.BPMNErrCandidateUsersEmpty); got == nil || got.Severity != domain.SeverityWarning {
		t.Errorf("Validate = %+v, want CANDIDATE_USERS_EMPTY as a warning on the module's task", errs)
	}

	errs, err = c.ValidateForPublish(context.Background(), unassigned)
	if err != nil {
		t.Fatalf("ValidateForPublish: %v", err)
	}
	got := findCode(blockingErrors(errs), domain.BPMNErrCandidateUsersEmpty)
	if got == nil || got.NodeID != "CA_1" || !strings.Contains(got.Message, "CA_1::Process_M/Process_M_Task") {
		t.Errorf("ValidateForPublish = %+v, want CANDIDATE_USERS_EMPTY on the call CA_1 naming node CA_1::Process_M/Process_M_Task", errs)
	}
	assertCompileRefusedWith(t, unassigned, domain.BPMNErrCandidateUsersEmpty)

	assigned := mainWithCalls([]callSite{
		{ID: "CA_1", Lane: "Engineering", Calls: "Process_M", Inputs: [][2]string{{"Assignees", `{"Process_M_Task":"` + otherUser + `"}`}}},
	}, moduleWithTask("Process_M", ""))
	if errs, err := c.ValidateForPublish(context.Background(), assigned); err != nil || len(blockingErrors(errs)) > 0 {
		t.Errorf("ValidateForPublish = %+v, %v; want a call naming the user to be publishable", errs, err)
	}
	compileForPublish(t, assigned)
}

func TestPublish_TopLevelTaskWithoutADefaultUser_IsRefused(t *testing.T) {
	bpmnXML := strings.Replace(lanedMainWithBoundary("Task_B", userTaskXML("Task_B")), `candidateUsers="`+validCandidateUser+`"`, `candidateUsers=""`, 1)
	errs, err := bpmn_compiler.New().ValidateForPublish(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("ValidateForPublish: %v", err)
	}
	if got := findCode(blockingErrors(errs), domain.BPMNErrCandidateUsersEmpty); got == nil || got.NodeID != "Task_A" {
		t.Errorf("errors = %+v, want CANDIDATE_USERS_EMPTY on Task_A", errs)
	}
}

func TestCompileForPublish_ExpandedStagesOverTheLimit_AreRefused(t *testing.T) {
	var tasks, flows strings.Builder
	prev := "Big_SubStart"
	for i := range 600 {
		fmt.Fprintf(&tasks, `      <bpmn:userTask id="Big_T%d"><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers=%q/></bpmn:extensionElements></bpmn:userTask>
`, i, validCandidateUser)
		fmt.Fprintf(&flows, "      <bpmn:sequenceFlow id=\"Big_SF%d\" sourceRef=%q targetRef=\"Big_T%d\"/>\n", i, prev, i)
		prev = fmt.Sprintf("Big_T%d", i)
	}
	big := fmt.Sprintf(`  <bpmn:process id="Process_Big" isExecutable="true">
    <bpmn:startEvent id="Big_Start"/>
    <bpmn:subProcess id="Big_Sub">
      <bpmn:startEvent id="Big_SubStart"/>
%s      <bpmn:endEvent id="Big_SubEnd"/>
%s      <bpmn:sequenceFlow id="Big_SFEnd" sourceRef=%q targetRef="Big_SubEnd"/>
    </bpmn:subProcess>
    <bpmn:endEvent id="Big_End"/>
    <bpmn:sequenceFlow id="Big_F1" sourceRef="Big_Start" targetRef="Big_Sub"/>
    <bpmn:sequenceFlow id="Big_F2" sourceRef="Big_Sub" targetRef="Big_End"/>
  </bpmn:process>`, tasks.String(), flows.String(), prev)
	bpmnXML := mainWithCalls([]callSite{
		{ID: "CA_1", Lane: "Engineering", Calls: "Process_Big"},
		{ID: "CA_2", Lane: "Ops", Calls: "Process_Big"},
	}, big)

	assertCompileRefusedWith(t, bpmnXML, domain.BPMNErrTaskLimitExceeded)
	errs, err := bpmn_compiler.New().ValidateForPublish(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("ValidateForPublish: %v", err)
	}
	if findCode(blockingErrors(errs), domain.BPMNErrTaskLimitExceeded) == nil {
		t.Errorf("ValidateForPublish = %+v, want TASK_LIMIT_EXCEEDED, as publish refuses it", errs)
	}
}

// workFreeLaneModule is Process_R with a lane, Starter, holding only its start
// event, and an open lane, Sender, holding its task.
func workFreeLaneModule() string {
	return fmt.Sprintf(`  <bpmn:process id="Process_R" isExecutable="true">
    <bpmn:laneSet id="Process_R_LaneSet">
      <bpmn:lane id="Process_R_Lane0" name="Starter"><bpmn:flowNodeRef>R_Start</bpmn:flowNodeRef></bpmn:lane>
      <bpmn:lane id="Process_R_Lane1" name="Sender"><bpmn:flowNodeRef>R_Send</bpmn:flowNodeRef><bpmn:flowNodeRef>R_End</bpmn:flowNodeRef></bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="R_Start"/>
    <bpmn:userTask id="R_Send"><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers=%q/></bpmn:extensionElements></bpmn:userTask>
    <bpmn:endEvent id="R_End"/>
    <bpmn:sequenceFlow id="R_F1" sourceRef="R_Start" targetRef="R_Send"/>
    <bpmn:sequenceFlow id="R_F2" sourceRef="R_Send" targetRef="R_End"/>
  </bpmn:process>`, validCandidateUser)
}

// subProcessLaneModule is Process_N whose one open lane, Nested, holds only a
// subProcess, which holds its task.
func subProcessLaneModule() string {
	return fmt.Sprintf(`  <bpmn:process id="Process_N" isExecutable="true">
    <bpmn:laneSet id="Process_N_LaneSet">
      <bpmn:lane id="Process_N_Lane0" name="Nested"><bpmn:flowNodeRef>N_Start</bpmn:flowNodeRef><bpmn:flowNodeRef>N_Sub</bpmn:flowNodeRef><bpmn:flowNodeRef>N_End</bpmn:flowNodeRef></bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="N_Start"/>
    <bpmn:subProcess id="N_Sub">
      <bpmn:startEvent id="N_SubStart"/>
      <bpmn:userTask id="N_Task"><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers=%q/></bpmn:extensionElements></bpmn:userTask>
      <bpmn:endEvent id="N_SubEnd"/>
      <bpmn:sequenceFlow id="N_SF1" sourceRef="N_SubStart" targetRef="N_Task"/>
      <bpmn:sequenceFlow id="N_SF2" sourceRef="N_Task" targetRef="N_SubEnd"/>
    </bpmn:subProcess>
    <bpmn:endEvent id="N_End"/>
    <bpmn:sequenceFlow id="N_F1" sourceRef="N_Start" targetRef="N_Sub"/>
    <bpmn:sequenceFlow id="N_F2" sourceRef="N_Sub" targetRef="N_End"/>
  </bpmn:process>`, validCandidateUser)
}

func TestCompileForPublish_ModuleWhoseOpenSlotOnlyCallsAnotherModule_Publishes(t *testing.T) {
	collab := compileForPublish(t, mainWithCalls([]callSite{{ID: "CA_1", Lane: "Ops", Calls: "Process_A"}},
		modulePassingTo("Process_A", "Process_F"), lanedModule("Process_F", moduleLane{"Legal", legalDeptID, "F_T"})))

	if got := departmentNamed(t, expandedMain(t, collab), "CA_1::Process_A_CA::Legal").IAMDepartmentID; got != legalDeptID {
		t.Errorf("the nested fixed lane runs in iam %q, want %s", got, legalDeptID)
	}
}

func TestCompileForPublish_OpenLaneHoldingOnlyASubProcess_IsBound(t *testing.T) {
	collab := compileForPublish(t, mainWithCalls([]callSite{{ID: "CA_1", Lane: "Ops", Calls: "Process_N"}}, subProcessLaneModule()))

	nested := departmentNamed(t, expandedMain(t, collab), "CA_1::Nested")
	if nested.IAMDepartmentID != opsDeptID {
		t.Errorf("the subProcess lane runs in iam %q, want the call's Ops %s", nested.IAMDepartmentID, opsDeptID)
	}
}

func TestCompileForPublish_CallKeepsItsOwnMappingsAndProperties(t *testing.T) {
	collab := compileForPublish(t, mainWithCalls([]callSite{{
		ID: "CA_1", Lane: "Engineering", Calls: "Process_M",
		Inputs:  [][2]string{{"clarificationId", "=clarificationId"}},
		Outputs: [][2]string{{"answer", "=result"}},
		Props:   [][2]string{{"priority", "high"}},
	}}, moduleWithTask("Process_M", validCandidateUser)))

	var step dsl.ExecutionStep
	for _, s := range planNamed(t, collab, collab.MainPlan).Execution.Steps {
		if s.CallPlan != nil {
			step = s
		}
	}
	want := &dsl.IOMapping{
		Inputs:  []dsl.IOVar{{Source: "=clarificationId", Target: "clarificationId"}},
		Outputs: []dsl.IOVar{{Source: "=result", Target: "answer"}},
	}
	if !reflect.DeepEqual(step.IOMapping, want) {
		t.Errorf("io_mapping = %+v, want %+v", step.IOMapping, want)
	}
	if step.Extras["priority"] != "high" {
		t.Errorf("extras = %v, want the call's priority property", step.Extras)
	}
}

func TestCompileForPublish_ModuleCalledOnlyInsideAParallelBranch(t *testing.T) {
	bpmnXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="Def_Par" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:laneSet id="LS">
      <bpmn:lane id="Lane_eng" name="Engineering"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value=%[1]q/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef><bpmn:flowNodeRef>Split</bpmn:flowNodeRef><bpmn:flowNodeRef>CA_1</bpmn:flowNodeRef><bpmn:flowNodeRef>Join</bpmn:flowNodeRef><bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
      </bpmn:lane>
      <bpmn:lane id="Lane_ops" name="Ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value=%[2]q/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Ops_Task</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1"/>
    <bpmn:parallelGateway id="Split"/>
    <bpmn:callActivity id="CA_1" name="Review"><bpmn:extensionElements><zeebe:calledElement processId="Process_M"/></bpmn:extensionElements></bpmn:callActivity>
    <bpmn:userTask id="Ops_Task"><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers=%[3]q/></bpmn:extensionElements></bpmn:userTask>
    <bpmn:parallelGateway id="Join"/>
    <bpmn:endEvent id="End_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start_1" targetRef="Split"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Split" targetRef="CA_1"/>
    <bpmn:sequenceFlow id="F3" sourceRef="Split" targetRef="Ops_Task"/>
    <bpmn:sequenceFlow id="F4" sourceRef="CA_1" targetRef="Join"/>
    <bpmn:sequenceFlow id="F5" sourceRef="Ops_Task" targetRef="Join"/>
    <bpmn:sequenceFlow id="F6" sourceRef="Join" targetRef="End_1"/>
  </bpmn:process>
%[4]s
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="Process_Main">
%[5]s  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`, engDeptID, opsDeptID, validCandidateUser, moduleWithTask("Process_M", validCandidateUser),
		shape("Lane_eng")+shape("Lane_ops")+shape("Start_1")+shape("Split")+shape("CA_1")+shape("Ops_Task")+shape("Join")+shape("End_1")+
			edge("F1")+edge("F2")+edge("F3")+edge("F4")+edge("F5")+edge("F6"))

	collab := compileForPublish(t, bpmnXML)
	planNamed(t, collab, "Process_M")
	if got := departmentNamed(t, expandedMain(t, collab), "CA_1::Process_M").IAMDepartmentID; got != engDeptID {
		t.Errorf("the branch's call runs in iam %q, want Engineering %s", got, engDeptID)
	}
}

// TestValidate_ModuleSlotBoundToALaneWithoutADepartment_IsRefused pins that
// every department a call binds in a workflow has an IAM department, whether
// Depts names the lane or the module's one open slot takes the call's own.
func TestValidate_ModuleSlotBoundToALaneWithoutADepartment_IsRefused(t *testing.T) {
	withoutOpsDept := func(bpmnXML string) string {
		return strings.Replace(bpmnXML, fmt.Sprintf(`<zeebe:property name="dept_id" value=%q/>`, opsDeptID), "", 1)
	}
	twoOpen := lanedModule("Process_R", moduleLane{"Sender", "", "R_Send"}, moduleLane{"Receiver", "", "R_Receive"})
	for name, bpmnXML := range map[string]string{
		"mapped by Depts": withoutOpsDept(mainWithCalls([]callSite{
			{ID: "CA_1", Lane: "Engineering", Calls: "Process_R", Inputs: [][2]string{{"Depts", `{"Sender":"Ops","Receiver":"Engineering"}`}}},
		}, twoOpen)),
		"the call's own lane": withoutOpsDept(mainWithCalls([]callSite{
			{ID: "CA_1", Lane: "Ops", Calls: "Process_M"},
		}, moduleWithTask("Process_M", validCandidateUser))),
	} {
		t.Run(name, func(t *testing.T) {
			got := findCode(validateBlocking(t, bpmnXML), domain.BPMNErrMissingDeptID)
			if got == nil || got.NodeID != "CA_1" || !strings.Contains(got.Message, "Ops") {
				t.Errorf("errors = %+v, want MISSING_DEPT_ID on CA_1 naming lane Ops", validateBlocking(t, bpmnXML))
			}
		})
	}
}

// TestCompileForPublish_CallSiteAssigneesReachATaskInsideAModuleSubProcess pins
// that Assignees finds a module task wherever the module's plan has it,
// including a subProcess body, as ExpandCalls does.
func TestCompileForPublish_CallSiteAssigneesReachATaskInsideAModuleSubProcess(t *testing.T) {
	module := `  <bpmn:process id="Process_S" isExecutable="true">
    <bpmn:startEvent id="S_Start"/>
    <bpmn:subProcess id="S_Sub">
      <bpmn:startEvent id="SS_Start"/>
      <bpmn:userTask id="SS_Task"><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparer"/></bpmn:extensionElements></bpmn:userTask>
      <bpmn:endEvent id="SS_End"/>
      <bpmn:sequenceFlow id="SS_F1" sourceRef="SS_Start" targetRef="SS_Task"/>
      <bpmn:sequenceFlow id="SS_F2" sourceRef="SS_Task" targetRef="SS_End"/>
    </bpmn:subProcess>
    <bpmn:endEvent id="S_End"/>
    <bpmn:sequenceFlow id="S_F1" sourceRef="S_Start" targetRef="S_Sub"/>
    <bpmn:sequenceFlow id="S_F2" sourceRef="S_Sub" targetRef="S_End"/>
  </bpmn:process>`
	bpmnXML := mainWithCalls([]callSite{
		{ID: "CA_1", Lane: "Engineering", Calls: "Process_S", Inputs: [][2]string{{"Assignees", `{"SS_Task":"` + otherUser + `"}`}}},
	}, module)
	errs, err := bpmn_compiler.New().ValidateForPublish(context.Background(), bpmnXML)
	if err != nil || len(blockingErrors(errs)) > 0 {
		t.Fatalf("ValidateForPublish = %+v, %v; want the subProcess task assignable", errs, err)
	}
	st := stageNamed(t, expandedMain(t, compileForPublish(t, bpmnXML)), "SS_Task")
	if !reflect.DeepEqual(st.DefaultAssignees, []string{otherUser}) {
		t.Errorf("SS_Task defaults = %v, want [%s]", st.DefaultAssignees, otherUser)
	}
}
