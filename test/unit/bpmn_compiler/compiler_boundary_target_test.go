package bpmn_compiler_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func userTaskXML(id string) string {
	return fmt.Sprintf(`    <bpmn:userTask id=%q><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers=%q/></bpmn:extensionElements></bpmn:userTask>
`, id, validCandidateUser)
}

func flowXML(id, from, to string) string {
	return fmt.Sprintf("    <bpmn:sequenceFlow id=%q sourceRef=%q targetRef=%q/>\n", id, from, to)
}

const timerP1D = `<bpmn:timerEventDefinition><bpmn:timeDuration>P1D</bpmn:timeDuration></bpmn:timerEventDefinition>`

// escalatingModule is a laneless module whose Task_Work has an interrupting
// timer that moves to Task_Escalate, and whose SP_Work has one that ends.
func escalatingModule(id string) string {
	return fmt.Sprintf(`  <bpmn:process id=%[1]q isExecutable="true">
    <bpmn:startEvent id="%[1]s_Start"/>
%[2]s%[3]s    <bpmn:boundaryEvent id="%[1]s_BTask" attachedToRef="Task_Work">%[4]s</bpmn:boundaryEvent>
    <bpmn:subProcess id="SP_Work">
      <bpmn:startEvent id="SP_Start"/>
      <bpmn:userTask id="SP_Task"><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers=%[5]q/></bpmn:extensionElements></bpmn:userTask>
      <bpmn:endEvent id="SP_End"/>
      <bpmn:sequenceFlow id="SP_F1" sourceRef="SP_Start" targetRef="SP_Task"/>
      <bpmn:sequenceFlow id="SP_F2" sourceRef="SP_Task" targetRef="SP_End"/>
    </bpmn:subProcess>
    <bpmn:boundaryEvent id="%[1]s_BSub" attachedToRef="SP_Work">%[4]s</bpmn:boundaryEvent>
    <bpmn:endEvent id="%[1]s_End"/>
    <bpmn:endEvent id="%[1]s_EndEsc"/>
    <bpmn:endEvent id="%[1]s_EndSub"/>
%[6]s  </bpmn:process>`, id, userTaskXML("Task_Work"), userTaskXML("Task_Escalate"), timerP1D, validCandidateUser,
		flowXML(id+"_F1", id+"_Start", "Task_Work")+
			flowXML(id+"_F2", "Task_Work", "SP_Work")+
			flowXML(id+"_F3", "SP_Work", id+"_End")+
			flowXML(id+"_F4", id+"_BTask", "Task_Escalate")+
			flowXML(id+"_F5", "Task_Escalate", id+"_EndEsc")+
			flowXML(id+"_F6", id+"_BSub", id+"_EndSub"))
}

func stageNamed(t *testing.T, p *dsl.CompiledPlan, nodeID string) dsl.StageDef {
	t.Helper()
	for _, d := range p.Departments {
		for _, s := range d.Stages {
			if s.NodeID == nodeID {
				return s
			}
		}
	}
	t.Fatalf("no stage %q in plan %q", nodeID, p.Name)
	return dsl.StageDef{}
}

// assertMovesTo fails unless department deptID of p is a segment of lane that
// starts at task nodeID: a boundary moving there runs that task first.
func assertMovesTo(t *testing.T, p *dsl.CompiledPlan, deptID, lane, nodeID string) {
	t.Helper()
	d := departmentNamed(t, p, deptID)
	if d.Label != lane || len(d.Stages) == 0 || d.Stages[0].NodeID != nodeID {
		t.Errorf("department %q = %+v, want a segment of %s starting at %s", deptID, d, lane, nodeID)
	}
}

// TestCompileForPublish_LanelessModuleBoundaries_NameTheirTarget pins that in
// a module with no lanes a boundary moving to a task targets a segment of the
// module's own department, and one ending at an end event terminates, instead
// of both compiling to an empty target.
func TestCompileForPublish_LanelessModuleBoundaries_NameTheirTarget(t *testing.T) {
	bpmnXML := mainWithCalls([]callSite{{ID: "CA_1", Lane: "Engineering", Calls: "Process_L"}}, escalatingModule("Process_L"))
	collab := compileForPublish(t, bpmnXML)
	module := planNamed(t, collab, "Process_L")

	work := stageNamed(t, module, "Task_Work")
	if work.BoundaryTimer == nil || work.BoundaryTimer.Terminates {
		t.Fatalf("Task_Work's timer = %+v, want a move to Task_Escalate", work.BoundaryTimer)
	}
	assertMovesTo(t, module, work.BoundaryTimer.TargetDept, "Process_L", "Task_Escalate")
	var sub *dsl.SubWorkflowStep
	for _, s := range module.Execution.Steps {
		if s.SubWorkflow != nil {
			sub = s.SubWorkflow
		}
	}
	if sub == nil || len(sub.TimerPaths) != 1 || !sub.TimerPaths[0].Terminates || sub.TimerPaths[0].TargetDept != "" {
		t.Fatalf("SP_Work's timer paths = %+v, want one that terminates with no target", sub)
	}

	expanded := expandedMain(t, collab)
	if got := stageNamed(t, expanded, "Task_Work").BoundaryTimer.TargetDept; got != "CA_1::"+work.BoundaryTimer.TargetDept {
		t.Errorf("expanded Task_Work's timer target = %q, want CA_1::%s", got, work.BoundaryTimer.TargetDept)
	}
}

// TestCompileForPublish_CallBoundariesToAnEndEvent_Terminate pins the
// explicit marker on every kind of call boundary that ends its path.
func TestCompileForPublish_CallBoundariesToAnEndEvent_Terminate(t *testing.T) {
	bpmnXML := mainWithCalls([]callSite{
		{ID: "CA_1", Lane: "Engineering", Calls: "Process_M", Boundaries: []string{"message", "timer", "error"}},
	}, moduleWithTask("Process_M", validCandidateUser))
	call := callSteps(planNamed(t, compileForPublish(t, bpmnXML), "Main"))[0]
	if len(call.MessagePaths) != 1 || !call.MessagePaths[0].Terminates ||
		len(call.TimerPaths) != 1 || !call.TimerPaths[0].Terminates ||
		len(call.ErrorPaths) != 1 || !call.ErrorPaths[0].Terminates {
		t.Errorf("call paths = %+v %+v %+v, want each to terminate", call.MessagePaths, call.TimerPaths, call.ErrorPaths)
	}
}

// lanedMainWithBoundary is a main process in one lane, Engineering, whose
// Task_A has an interrupting timer leading to target, the first of extra.
func lanedMainWithBoundary(target string, extra string, procs ...string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="Def_B" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="Msg_n" name="notify"/>
  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:laneSet id="LS"><bpmn:lane id="Lane_eng" name="Engineering"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value=%[1]q/></zeebe:properties></bpmn:extensionElements>
      <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef><bpmn:flowNodeRef>Task_A</bpmn:flowNodeRef><bpmn:flowNodeRef>B_A</bpmn:flowNodeRef><bpmn:flowNodeRef>%[2]s</bpmn:flowNodeRef><bpmn:flowNodeRef>End_1</bpmn:flowNodeRef><bpmn:flowNodeRef>End_2</bpmn:flowNodeRef>
    </bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="Start_1"/>
%[3]s    <bpmn:boundaryEvent id="B_A" attachedToRef="Task_A">%[4]s</bpmn:boundaryEvent>
%[5]s    <bpmn:endEvent id="End_1"/>
    <bpmn:endEvent id="End_2"/>
%[6]s  </bpmn:process>
%[7]s
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="Process_Main">
%[8]s  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`, engDeptID, target, userTaskXML("Task_A"), timerP1D, extra,
		flowXML("F1", "Start_1", "Task_A")+flowXML("F2", "Task_A", "End_1")+flowXML("F3", "B_A", target)+flowXML("F4", target, "End_2"),
		strings.Join(procs, "\n"), diagramOf("Lane_eng", "Start_1", "Task_A", "B_A", target, "End_1", "End_2", "F1", "F2", "F3", "F4"))
}

func diagramOf(ids ...string) string {
	var b strings.Builder
	for _, id := range ids {
		if strings.HasPrefix(id, "F") {
			b.WriteString(edge(id))
		} else {
			b.WriteString(shape(id))
		}
	}
	return b.String()
}

// TestValidate_BoundaryLeadingToACallOrMessageTask_IsRefused pins that a
// boundary whose path starts at a node the compiler cannot target is refused
// rather than compiled as if it ended its path.
func TestValidate_BoundaryLeadingToACallOrMessageTask_IsRefused(t *testing.T) {
	for _, tc := range []struct {
		name, target, extra string
		procs               []string
	}{
		{"callActivity", "CA_Esc", `    <bpmn:callActivity id="CA_Esc"><bpmn:extensionElements><zeebe:calledElement processId="Process_M"/></bpmn:extensionElements></bpmn:callActivity>
`, []string{moduleWithTask("Process_M", validCandidateUser)}},
		{"sendTask", "Send_N", `    <bpmn:sendTask id="Send_N" messageRef="Msg_n"><bpmn:extensionElements><zeebe:taskDefinition type="send"/><zeebe:assignmentDefinition candidateGroups="preparer"/></bpmn:extensionElements></bpmn:sendTask>
`, nil},
		{"receiveTask", "Recv_N", `    <bpmn:receiveTask id="Recv_N" messageRef="Msg_n"><bpmn:extensionElements><zeebe:taskDefinition type="receive"/><zeebe:assignmentDefinition candidateGroups="preparer"/></bpmn:extensionElements></bpmn:receiveTask>
`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := validateBlocking(t, lanedMainWithBoundary(tc.target, tc.extra, tc.procs...))
			found := false
			for _, e := range errs {
				if e.Code == domain.BPMNErrUnsupportedBoundaryTarget && e.NodeID == "B_A" {
					found = true
				}
			}
			if !found {
				t.Errorf("errors = %+v, want %s on B_A", errs, domain.BPMNErrUnsupportedBoundaryTarget)
			}
		})
	}
}

// TestCompileForPublish_BoundaryInsideASubProcessBody_TargetsItsLane pins
// that a boundary between two tasks of a subProcess body, which has no lanes
// of its own, moves to the subProcess's lane.
func TestCompileForPublish_BoundaryInsideASubProcessBody_TargetsItsLane(t *testing.T) {
	bpmnXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="Def_S" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:laneSet id="LS"><bpmn:lane id="Lane_eng" name="Engineering"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value=%[1]q/></zeebe:properties></bpmn:extensionElements>
      <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef><bpmn:flowNodeRef>SP_1</bpmn:flowNodeRef><bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
    </bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="Start_1"/>
    <bpmn:subProcess id="SP_1">
      <bpmn:startEvent id="SP_Start"/>
%[2]s%[3]s      <bpmn:boundaryEvent id="SP_B" attachedToRef="Task_In">%[4]s</bpmn:boundaryEvent>
      <bpmn:endEvent id="SP_End"/>
      <bpmn:endEvent id="SP_EndEsc"/>
%[5]s    </bpmn:subProcess>
    <bpmn:endEvent id="End_1"/>
%[6]s  </bpmn:process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="Process_Main">
%[7]s  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`, engDeptID, userTaskXML("Task_In"), userTaskXML("Task_InEsc"), timerP1D,
		flowXML("SF1", "SP_Start", "Task_In")+flowXML("SF2", "Task_In", "SP_End")+flowXML("SF3", "SP_B", "Task_InEsc")+flowXML("SF4", "Task_InEsc", "SP_EndEsc"),
		flowXML("F1", "Start_1", "SP_1")+flowXML("F2", "SP_1", "End_1"),
		diagramOf("Lane_eng", "Start_1", "SP_1", "End_1", "F1", "F2", "SP_Start", "Task_In", "Task_InEsc", "SP_B", "SP_End", "SP_EndEsc")+edge("SF1")+edge("SF2")+edge("SF3")+edge("SF4"))
	main := planNamed(t, compileForPublish(t, bpmnXML), "Main")
	got := stageNamed(t, main, "Task_In").BoundaryTimer
	if got == nil || got.Terminates {
		t.Fatalf("Task_In's timer = %+v, want a move to Task_InEsc", got)
	}
	assertMovesTo(t, main, got.TargetDept, "Engineering", "Task_InEsc")
}

// lanelessRoot is a workflow with no lanes: Task_A, and, with boundary set, an
// interrupting timer on it leading to Task_B.
func lanelessRoot(boundary bool) string {
	extra, flows, ids := "", flowXML("F1", "Start_1", "Task_A")+flowXML("F2", "Task_A", "End_1"), []string{"Start_1", "Task_A", "End_1", "F1", "F2"}
	if boundary {
		extra = userTaskXML("Task_B") + `    <bpmn:boundaryEvent id="B_A" attachedToRef="Task_A">` + timerP1D + `</bpmn:boundaryEvent>
    <bpmn:endEvent id="End_2"/>
`
		flows += flowXML("F3", "B_A", "Task_B") + flowXML("F4", "Task_B", "End_2")
		ids = append(ids, "Task_B", "B_A", "End_2", "F3", "F4")
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="Def_L" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:startEvent id="Start_1"/>
%s%s    <bpmn:endEvent id="End_1"/>
%s  </bpmn:process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="Process_Main">
%s  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`, userTaskXML("Task_A"), extra, flows, diagramOf(ids...))
}

// TestPublish_WorkflowWithoutLanes_IsRefused pins that a workflow whose tasks
// sit in no lane, and so in no IAM department, is refused as publish refuses
// it, with or without a boundary, and never as an internal error.
func TestPublish_WorkflowWithoutLanes_IsRefused(t *testing.T) {
	for _, boundary := range []bool{false, true} {
		t.Run(fmt.Sprintf("boundary=%v", boundary), func(t *testing.T) {
			bpmnXML := lanelessRoot(boundary)
			errs, err := bpmn_compiler.New().ValidateForPublish(context.Background(), bpmnXML)
			if err != nil {
				t.Fatalf("ValidateForPublish returned an error, not a validation result: %v", err)
			}
			if got := findCode(blockingErrors(errs), domain.BPMNErrMissingDeptID); got == nil || got.NodeID != "Task_A" {
				t.Errorf("errors = %+v, want MISSING_DEPT_ID on Task_A", errs)
			}
			assertCompileRefusedWith(t, bpmnXML, domain.BPMNErrMissingDeptID)
		})
	}
}

func connectorXML(id string) string {
	return fmt.Sprintf("    <bpmn:serviceTask id=%q><bpmn:extensionElements><zeebe:taskDefinition type=\"connector:storage\"/></bpmn:extensionElements></bpmn:serviceTask>\n", id)
}

// lanelessWorkflow wraps nodes and flows, and procs as further processes, as
// a workflow with no lanes; ids are the main process's elements for the
// diagram.
func lanelessWorkflow(nodes, flows string, ids []string, procs ...string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="Def_W" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:startEvent id="Start_1"/>
%s    <bpmn:endEvent id="End_1"/>
%s  </bpmn:process>
%s
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="Process_Main">
%s  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`, nodes, flows, strings.Join(procs, "\n"), diagramOf(append([]string{"Start_1", "End_1"}, ids...)...))
}

// TestPublish_WorkflowWithoutLanes_IsRefusedWhateverItsWork pins the refusal
// for a workflow with no lanes whose work is only a call, or only connector
// tasks with a boundary between them: every such document is a validation
// result, never an internal error, and none publishes work with no IAM
// department.
func TestPublish_WorkflowWithoutLanes_IsRefusedWhateverItsWork(t *testing.T) {
	for name, tc := range map[string]struct {
		bpmnXML, node string
	}{
		"only a call": {lanelessWorkflow(
			`    <bpmn:callActivity id="CA_1"><bpmn:extensionElements><zeebe:calledElement processId="Process_M"/></bpmn:extensionElements></bpmn:callActivity>
`, flowXML("F1", "Start_1", "CA_1")+flowXML("F2", "CA_1", "End_1"), []string{"CA_1", "F1", "F2"},
			moduleWithTask("Process_M", validCandidateUser)), "CA_1"},
		"only connectors, with a boundary": {lanelessWorkflow(
			connectorXML("C_1")+connectorXML("C_2")+`    <bpmn:boundaryEvent id="B_C" attachedToRef="C_1">`+timerP1D+`</bpmn:boundaryEvent>
    <bpmn:endEvent id="End_2"/>
`, flowXML("F1", "Start_1", "C_1")+flowXML("F2", "C_1", "End_1")+flowXML("F3", "B_C", "C_2")+flowXML("F4", "C_2", "End_2"),
			[]string{"C_1", "C_2", "B_C", "End_2", "F1", "F2", "F3", "F4"}), "C_1"},
	} {
		t.Run(name, func(t *testing.T) {
			errs, err := bpmn_compiler.New().ValidateForPublish(context.Background(), tc.bpmnXML)
			if err != nil {
				t.Fatalf("ValidateForPublish returned an error, not a validation result: %v", err)
			}
			if got := findCode(blockingErrors(errs), domain.BPMNErrMissingDeptID); got == nil {
				t.Errorf("errors = %+v, want MISSING_DEPT_ID", errs)
			} else if !slicesContainNode(blockingErrors(errs), domain.BPMNErrMissingDeptID, tc.node) {
				t.Errorf("errors = %+v, want MISSING_DEPT_ID on %s", errs, tc.node)
			}
			assertCompileRefusedWith(t, tc.bpmnXML, domain.BPMNErrMissingDeptID)
		})
	}
}

func slicesContainNode(errs []domain.BPMNValidationError, code domain.BPMNErrorCode, node string) bool {
	for _, e := range errs {
		if e.Code == code && e.NodeID == node {
			return true
		}
	}
	return false
}
