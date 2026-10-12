package bpmn_compiler_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
)

// twoLaneBoundaryPath is a workflow whose Task_A, in Engineering, has an
// interrupting timer leading through Task_B (Engineering again) and Task_C
// (Ops) to an end event: a path of three tasks over two lanes.
func twoLaneBoundaryPath() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="Def_P" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:laneSet id="LS">
      <bpmn:lane id="Lane_eng" name="Engineering"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value=%[1]q/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef><bpmn:flowNodeRef>Task_A</bpmn:flowNodeRef><bpmn:flowNodeRef>B_A</bpmn:flowNodeRef><bpmn:flowNodeRef>Task_B</bpmn:flowNodeRef><bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
      </bpmn:lane>
      <bpmn:lane id="Lane_ops" name="Ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value=%[2]q/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Task_C</bpmn:flowNodeRef><bpmn:flowNodeRef>End_2</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1"/>
%[3]s%[4]s%[5]s    <bpmn:boundaryEvent id="B_A" attachedToRef="Task_A">%[6]s</bpmn:boundaryEvent>
    <bpmn:endEvent id="End_1"/>
    <bpmn:endEvent id="End_2"/>
%[7]s  </bpmn:process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="Process_Main">
%[8]s  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`, engDeptID, opsDeptID, userTaskXML("Task_A"), userTaskXML("Task_B"), userTaskXML("Task_C"), timerP1D,
		flowXML("F1", "Start_1", "Task_A")+flowXML("F2", "Task_A", "End_1")+flowXML("F3", "B_A", "Task_B")+flowXML("F4", "Task_B", "Task_C")+flowXML("F5", "Task_C", "End_2"),
		diagramOf("Lane_eng", "Lane_ops", "Start_1", "Task_A", "B_A", "Task_B", "Task_C", "End_1", "End_2", "F1", "F2", "F3", "F4", "F5"))
}

// sequentialDepartments flattens the departments steps run in order.
func sequentialDepartments(steps []dsl.ExecutionStep) []string {
	var out []string
	for _, s := range steps {
		out = append(out, s.Sequential...)
	}
	return out
}

// TestCompileForPublish_BoundaryCarriesTheWholePathItLeadsTo pins that a
// boundary names every department of its path, in the order the flows give,
// not only the first: the Engineering task after the timer and the Ops task
// after that.
func TestCompileForPublish_BoundaryCarriesTheWholePathItLeadsTo(t *testing.T) {
	main := planNamed(t, compileForPublish(t, twoLaneBoundaryPath()), "Main")

	timer := stageNamed(t, main, "Task_A").BoundaryTimer
	if timer == nil {
		t.Fatal("Task_A has no timer boundary")
	}
	if timer.BoundaryID != "B_A" {
		t.Errorf("boundary id = %q, want B_A", timer.BoundaryID)
	}
	path := sequentialDepartments(timer.Steps)
	if len(path) != 2 {
		t.Fatalf("the timer's path = %v, want the Engineering segment of Task_B then Ops", path)
	}
	assertMovesTo(t, main, path[0], "Engineering", "Task_B")
	assertMovesTo(t, main, path[1], "Ops", "Task_C")
	if path[0] != timer.TargetDept {
		t.Errorf("TargetDept = %q, want the path's first department %q", timer.TargetDept, path[0])
	}
}

// TestCompileForPublish_CallAndSubProcessBoundariesCarryTheirPath pins the
// same for boundaries on a subProcess and on a call.
func TestCompileForPublish_CallAndSubProcessBoundariesCarryTheirPath(t *testing.T) {
	collab := compileForPublish(t, mainWithCalls([]callSite{{ID: "CA_1", Lane: "Engineering", Calls: "Process_L"}}, escalatingModule("Process_L")))
	module := planNamed(t, collab, "Process_L")

	work := stageNamed(t, module, "Task_Work")
	if got := sequentialDepartments(work.BoundaryTimer.Steps); len(got) != 1 || got[0] != work.BoundaryTimer.TargetDept {
		t.Errorf("Task_Work's timer steps = %v, want its one target department %q", got, work.BoundaryTimer.TargetDept)
	}
	var sub *dsl.SubWorkflowStep
	for _, s := range module.Execution.Steps {
		if s.SubWorkflow != nil {
			sub = s.SubWorkflow
		}
	}
	if sub == nil || len(sub.TimerPaths) != 1 || sub.TimerPaths[0].BoundaryID != "Process_L_BSub" || len(sub.TimerPaths[0].Steps) != 0 {
		t.Errorf("SP_Work's timer paths = %+v, want one with its boundary id and no steps, since it ends at once", sub)
	}
}

// rejoiningBoundary is lanedMainWithBoundary whose boundary path, Task_B, flows
// into Task_M, the task the main flow also goes on to: the path rejoins it.
// With rejoin false, Task_B ends at its own end event instead.
func rejoiningBoundary(rejoin bool) string {
	if !rejoin {
		return lanedMainWithBoundary("Task_B", userTaskXML("Task_B"))
	}
	doc := lanedMainWithBoundary("Task_B", userTaskXML("Task_B")+userTaskXML("Task_M"))
	return strings.NewReplacer(
		`sourceRef="Task_A" targetRef="End_1"/>`, `sourceRef="Task_A" targetRef="Task_M"/>`,
		`sourceRef="Task_B" targetRef="End_2"/>`, `sourceRef="Task_B" targetRef="Task_M"/>`,
		`    <bpmn:endEvent id="End_1"/>`, `    <bpmn:endEvent id="End_1"/>`+"\n"+`    <bpmn:sequenceFlow id="F5" sourceRef="Task_M" targetRef="End_1"/>`,
		`<bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>`, `<bpmn:flowNodeRef>End_1</bpmn:flowNodeRef><bpmn:flowNodeRef>Task_M</bpmn:flowNodeRef>`,
		`  </bpmndi:BPMNPlane>`, shape("Task_M")+edge("F5")+`  </bpmndi:BPMNPlane>`,
		`    <bpmn:endEvent id="End_2"/>`+"\n", ``,
		`<bpmn:flowNodeRef>End_2</bpmn:flowNodeRef>`, ``,
		shape("End_2"), ``,
	).Replace(doc)
}

// TestCompileForPublish_BoundaryPathRejoiningTheMainFlow_KeepsItsDepartmentOnly
// pins that a boundary path flowing into a task the main flow also reaches
// carries no steps: it names the department it moves to and nothing more, as
// before paths were carried, so the flow after the host is not cut short by a
// path that was meant to continue into it.
func TestCompileForPublish_BoundaryPathRejoiningTheMainFlow_KeepsItsDepartmentOnly(t *testing.T) {
	main := planNamed(t, compileForPublish(t, rejoiningBoundary(true)), "Main")

	timer := stageNamed(t, main, "Task_A").BoundaryTimer
	if timer == nil || timer.Terminates || timer.TargetDept == "" {
		t.Fatalf("Task_A's timer = %+v, want a move to the department of Task_B", timer)
	}
	if len(timer.Steps) != 0 {
		t.Errorf("steps = %+v, want none for a path that rejoins the main flow", timer.Steps)
	}

	ends := planNamed(t, compileForPublish(t, rejoiningBoundary(false)), "Main")
	if got := stageNamed(t, ends, "Task_A").BoundaryTimer; len(got.Steps) == 0 {
		t.Errorf("a path ending at its own end event must carry its steps: %+v", got)
	}
}

// TestCompileForPublish_BoundaryPathRejoiningThroughAGateway_KeepsItsDepartmentOnly
// pins the same when the way back into the main flow is one branch of a gateway
// on the path: the branch must not recompile, and so re-run, the main flow's task.
func TestCompileForPublish_BoundaryPathRejoiningThroughAGateway_KeepsItsDepartmentOnly(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + userTaskXML("B") + userTaskXML("Esc") + "    <bpmn:exclusiveGateway id=\"XG\"/>\n" +
		"    <bpmn:boundaryEvent id=\"BE\" attachedToRef=\"A\">" + timerP1D + "</bpmn:boundaryEvent>\n    <bpmn:endEvent id=\"E\"/>\n    <bpmn:endEvent id=\"E2\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "B") + flowXML("F3", "B", "E") +
		flowXML("F4", "BE", "Esc") + flowXML("F5", "Esc", "XG") + conditionalFlowXML("F6", "XG", "B", "=r") + flowXML("F7", "XG", "E2")
	main := planNamed(t, compileForPublish(t, flowDocument([]flowLane{
		{"Engineering", engDeptID, []string{"S", "A", "E", "E2", "BE", "XG"}},
		{"Ops", opsDeptID, []string{"B", "Esc"}},
	}, body)), "Main")

	timer := stageNamed(t, main, "A").BoundaryTimer
	if timer == nil {
		t.Fatal("A has no timer boundary")
	}
	if len(timer.Steps) != 0 {
		t.Errorf("steps = %+v, want none: a branch of the path continues into the main flow's B", timer.Steps)
	}
}
