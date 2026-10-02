package bpmn_compiler_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// flowLane is one lane of flowDocument, holding the listed nodes.
type flowLane struct {
	Name, DeptID string
	Nodes        []string
}

var flowNodeID = regexp.MustCompile(`<bpmn:(\w+) id="([^"]+)"`)

// flowDocument is a main process with the given lanes and body, with procs
// appended as further processes and a diagram element for every node and flow.
func flowDocument(lanes []flowLane, body string, procs ...string) string {
	var laneXML, diagram strings.Builder
	for i, l := range lanes {
		fmt.Fprintf(&laneXML, "      <bpmn:lane id=\"Lane_%d\" name=%q><bpmn:extensionElements><zeebe:properties><zeebe:property name=\"dept_id\" value=%q/></zeebe:properties></bpmn:extensionElements>", i, l.Name, l.DeptID)
		for _, n := range l.Nodes {
			fmt.Fprintf(&laneXML, "<bpmn:flowNodeRef>%s</bpmn:flowNodeRef>", n)
		}
		laneXML.WriteString("</bpmn:lane>\n")
		diagram.WriteString(shape(fmt.Sprintf("Lane_%d", i)))
	}
	for _, m := range flowNodeID.FindAllStringSubmatch(body, -1) {
		switch {
		case strings.HasSuffix(m[1], "EventDefinition"):
		case m[1] == "sequenceFlow":
			diagram.WriteString(edge(m[2]))
		default:
			diagram.WriteString(shape(m[2]))
		}
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="Def_Flow" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
%s    </bpmn:laneSet>
%s  </bpmn:process>
%s
  <bpmndi:BPMNDiagram id="BPMNDiagram_1"><bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Process_Main">
%s  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`, laneXML.String(), body, strings.Join(procs, "\n"), diagram.String())
}

func conditionalFlowXML(id, from, to, condition string) string {
	return fmt.Sprintf("    <bpmn:sequenceFlow id=%q sourceRef=%q targetRef=%q><bpmn:conditionExpression>%s</bpmn:conditionExpression></bpmn:sequenceFlow>\n", id, from, to, condition)
}

func callXML(id, processID string) string {
	return fmt.Sprintf("    <bpmn:callActivity id=%q><bpmn:extensionElements><zeebe:calledElement processId=%q/></bpmn:extensionElements></bpmn:callActivity>\n", id, processID)
}

// runOrder lists the tasks the expanded main plan runs, in order, following
// the Execution Service's rules: a department runs its stages in order, an
// exclusive branch runs its Steps, or else its target department from its
// target stage on, and parallel branches are listed one after another. choices picks the forward
// branch, by index, of each exclusive step met.
func runOrder(t *testing.T, collab *dsl.CompiledCollaboration, choices ...int) []string {
	t.Helper()
	r := &planRun{t: t, plan: expandedMain(t, collab), choices: choices}
	r.steps(r.plan.Execution.Steps)
	return r.order
}

type planRun struct {
	t       *testing.T
	plan    *dsl.CompiledPlan
	choices []int
	order   []string
}

func (r *planRun) steps(steps []dsl.ExecutionStep) (terminated bool) {
	for _, s := range steps {
		switch {
		case len(s.Sequential) > 0:
			for _, d := range s.Sequential {
				r.department(d, "")
			}
		case len(s.Parallel) > 0:
			for _, b := range s.Parallel {
				r.steps(b.Steps)
			}
		case len(s.Exclusive) > 0:
			if r.exclusive(s.Exclusive) {
				return true
			}
		case s.SubWorkflow != nil:
			r.steps(s.SubWorkflow.Plan.Steps)
		}
	}
	return false
}

func (r *planRun) exclusive(branches []dsl.ExclusiveBranch) (terminated bool) {
	forward := slices.DeleteFunc(slices.Clone(branches), func(b dsl.ExclusiveBranch) bool { return b.RevertToDept != "" })
	if len(r.choices) == 0 {
		r.t.Fatalf("an exclusive step with %d forward branches was met with no choice left", len(forward))
	}
	b := forward[r.choices[0]]
	r.choices = r.choices[1:]
	switch {
	case len(b.Steps) > 0:
		return r.steps(b.Steps) || b.Terminates
	case b.Terminates:
		return true
	case b.Target == "":
		return false
	case b.TargetNodeID != "":
		r.department(b.Target, b.TargetNodeID)
	default:
		r.department(b.Target, b.TargetStage)
	}
	return false
}

// department runs deptID's stages from the one whose node key ends in from,
// or from the first when from is empty. A key no stage has fails the
// instance in the Execution Service; here it is listed as "missing:<key>".
func (r *planRun) department(deptID, from string) {
	i := slices.IndexFunc(r.plan.Departments, func(d dsl.DepartmentDef) bool { return d.ID == deptID })
	if i < 0 {
		r.order = append(r.order, "missing:"+deptID)
		return
	}
	stages := r.plan.Departments[i].Stages
	start := 0
	if from != "" {
		start = slices.IndexFunc(stages, func(s dsl.StageDef) bool { return dsl.NodeKey(deptID, &s) == deptID+"/"+from })
		if start < 0 {
			r.order = append(r.order, "missing:"+deptID+"/"+from)
			return
		}
	}
	for _, s := range stages[start:] {
		r.order = append(r.order, s.NodeID)
	}
}

func assertRunOrder(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("tasks run in order %v, want %v", got, want)
	}
}

// TestCompileForPublish_FlowReturningToALane_RunsInFlowOrder pins that the
// arrows decide the order: a lane says who does a task, never when.
func TestCompileForPublish_FlowReturningToALane_RunsInFlowOrder(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + userTaskXML("B") + userTaskXML("C") + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "B") + flowXML("F3", "B", "C") + flowXML("F4", "C", "E")
	collab := compileForPublish(t, flowDocument([]flowLane{
		{"Engineering", engDeptID, []string{"S", "A", "C", "E"}},
		{"Ops", opsDeptID, []string{"B"}},
	}, body))

	assertRunOrder(t, runOrder(t, collab), "A", "B", "C")
}

// exclusiveWithJoin is S → A → X1 → {first | C} → J1 → D → E, with first
// taken when x holds and A, D in Engineering and C in Ops.
func exclusiveWithJoin(first, firstXML string, firstLane []string, procs ...string) string {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + "    <bpmn:exclusiveGateway id=\"X1\"/>\n" + firstXML + userTaskXML("C") +
		"    <bpmn:exclusiveGateway id=\"J1\"/>\n" + userTaskXML("D") + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "X1") + conditionalFlowXML("F3", "X1", first, "=x") + flowXML("F4", "X1", "C") +
		flowXML("F5", first, "J1") + flowXML("F6", "C", "J1") + flowXML("F7", "J1", "D") + flowXML("F8", "D", "E")
	return flowDocument([]flowLane{
		{"Engineering", engDeptID, append([]string{"S", "A", "X1", "J1", "D", "E"}, firstLane...)},
		{"Ops", opsDeptID, []string{"C"}},
	}, body, procs...)
}

func TestCompileForPublish_ExclusiveBranches_RunOnlyTheirOwnTasks(t *testing.T) {
	collab := compileForPublish(t, exclusiveWithJoin("B", userTaskXML("B"), []string{"B"}))

	assertRunOrder(t, runOrder(t, collab, 0), "A", "B", "D")
	assertRunOrder(t, runOrder(t, collab, 1), "A", "C", "D")
}

func TestCompileForPublish_CallOnAnExclusiveBranch_RunsItsModuleOnThatBranchOnly(t *testing.T) {
	collab := compileForPublish(t, exclusiveWithJoin("CA", callXML("CA", "Mod"), []string{"CA"}, lanedModule("Mod", moduleLane{"Rev", "", "M_T"})))

	assertRunOrder(t, runOrder(t, collab, 0), "A", "M_T", "D")
	assertRunOrder(t, runOrder(t, collab, 1), "A", "C", "D")
}

func TestCompileForPublish_SubProcessOnAnExclusiveBranch_RunsOnThatBranchOnly(t *testing.T) {
	sub := "    <bpmn:subProcess id=\"SP\"><bpmn:startEvent id=\"SP_S\"/>\n" + userTaskXML("SP_T") + "    <bpmn:endEvent id=\"SP_E\"/>\n" +
		flowXML("SP_F1", "SP_S", "SP_T") + flowXML("SP_F2", "SP_T", "SP_E") + "    </bpmn:subProcess>\n"
	collab := compileForPublish(t, exclusiveWithJoin("SP", sub, []string{"SP"}))

	assertRunOrder(t, runOrder(t, collab, 0), "A", "SP_T", "D")
	assertRunOrder(t, runOrder(t, collab, 1), "A", "C", "D")
}

// TestValidateForPublish_NestedAssigneesThroughACallOnAnExclusiveBranch pins
// that a module's own exclusive-branch call is in the plan, so an Assignees
// path through it names the inner module's task rather than failing.
func TestValidateForPublish_NestedAssigneesThroughACallOnAnExclusiveBranch(t *testing.T) {
	outer := `  <bpmn:process id="Mod2" isExecutable="true">
    <bpmn:laneSet id="M2_LS"><bpmn:lane id="M2_L" name="Rev"><bpmn:flowNodeRef>M2_S</bpmn:flowNodeRef><bpmn:flowNodeRef>M2_X</bpmn:flowNodeRef><bpmn:flowNodeRef>Inner</bpmn:flowNodeRef><bpmn:flowNodeRef>M2_T</bpmn:flowNodeRef><bpmn:flowNodeRef>M2_J</bpmn:flowNodeRef><bpmn:flowNodeRef>M2_E</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet>
    <bpmn:startEvent id="M2_S"/><bpmn:exclusiveGateway id="M2_X"/>
` + callXML("Inner", "Mod3") + userTaskXML("M2_T") + `    <bpmn:exclusiveGateway id="M2_J"/><bpmn:endEvent id="M2_E"/>
` + flowXML("M2_F1", "M2_S", "M2_X") + conditionalFlowXML("M2_F2", "M2_X", "Inner", "=x") + flowXML("M2_F3", "M2_X", "M2_T") +
		flowXML("M2_F4", "Inner", "M2_J") + flowXML("M2_F5", "M2_T", "M2_J") + flowXML("M2_F6", "M2_J", "M2_E") + "  </bpmn:process>"
	inner := strings.Replace(lanedModule("Mod3", moduleLane{"Rev3", "", "M3_T"}), ` candidateUsers="`+validCandidateUser+`"`, "", 1)
	call := fmt.Sprintf(`    <bpmn:callActivity id="CA"><bpmn:extensionElements><zeebe:calledElement processId="Mod2"/><zeebe:ioMapping><zeebe:input source="{&quot;Inner::M3_T&quot;:&quot;%s&quot;}" target="Assignees"/></zeebe:ioMapping></bpmn:extensionElements></bpmn:callActivity>
`, validCandidateUser)
	body := "    <bpmn:startEvent id=\"S\"/>\n" + call + "    <bpmn:endEvent id=\"E\"/>\n" + flowXML("F1", "S", "CA") + flowXML("F2", "CA", "E")
	bpmnXML := flowDocument([]flowLane{{"Engineering", engDeptID, []string{"S", "CA", "E"}}}, body, outer, inner)

	errs, err := bpmn_compiler.New().ValidateForPublish(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("ValidateForPublish: %v", err)
	}
	if blocking := blockingErrors(errs); len(blocking) > 0 {
		t.Errorf("issues = %+v, want none", blocking)
	}
	assertRunOrder(t, runOrder(t, compileForPublish(t, bpmnXML), 0), "M3_T")
}

// twoLevelFork is S → A → P1 → {X1 | P2 → {X2 | B} → J2} → J1 → C → E, with
// P1's flows to X1 and P2 listed in the given order.
func twoLevelFork(innerFirst bool) string {
	toX1, toP2 := flowXML("F3", "P1", "X1"), flowXML("F4", "P1", "P2")
	if innerFirst {
		toX1, toP2 = toP2, toX1
	}
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + "    <bpmn:parallelGateway id=\"P1\"/>\n" + userTaskXML("X1") +
		"    <bpmn:parallelGateway id=\"P2\"/>\n" + userTaskXML("X2") + userTaskXML("B") +
		"    <bpmn:parallelGateway id=\"J2\"/>\n    <bpmn:parallelGateway id=\"J1\"/>\n" + userTaskXML("C") + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "P1") + toX1 + toP2 + flowXML("F5", "P2", "X2") + flowXML("F6", "P2", "B") +
		flowXML("F7", "X2", "J2") + flowXML("F8", "B", "J2") + flowXML("F9", "J2", "J1") + flowXML("F10", "X1", "J1") +
		flowXML("F11", "J1", "C") + flowXML("F12", "C", "E")
	return flowDocument([]flowLane{
		{"Engineering", engDeptID, []string{"S", "A", "P1", "P2", "J1", "J2", "C", "E"}},
		{"Ops", opsDeptID, []string{"X1", "X2", "B"}},
	}, body)
}

func TestCompileForPublish_TwoLevelFork_RunsEachTaskOnceWhateverTheFlowOrder(t *testing.T) {
	for _, innerFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("inner fork listed first=%v", innerFirst), func(t *testing.T) {
			got := runOrder(t, compileForPublish(t, twoLevelFork(innerFirst)))
			sorted := slices.Sorted(slices.Values(got))
			if !slices.Equal(sorted, []string{"A", "B", "C", "X1", "X2"}) || got[0] != "A" || got[len(got)-1] != "C" {
				t.Errorf("tasks run = %v, want A, then X1, X2 and B once each, then C", got)
			}
		})
	}
}

// TestCompileForPublish_ParallelBranchesInOneLane_AreDistinct pins that two
// branches whose tasks share a lane are two branches, each running only its
// own task: the Execution Service tells branches apart by their department.
func TestCompileForPublish_ParallelBranchesInOneLane_AreDistinct(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + "    <bpmn:parallelGateway id=\"P1\"/>\n" + userTaskXML("B") + userTaskXML("C") +
		"    <bpmn:parallelGateway id=\"J1\"/>\n" + userTaskXML("D") + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "P1") + flowXML("F3", "P1", "B") + flowXML("F4", "P1", "C") +
		flowXML("F5", "B", "J1") + flowXML("F6", "C", "J1") + flowXML("F7", "J1", "D") + flowXML("F8", "D", "E")
	collab := compileForPublish(t, flowDocument([]flowLane{
		{"Engineering", engDeptID, []string{"S", "A", "P1", "J1", "D", "E"}},
		{"Ops", opsDeptID, []string{"B", "C"}},
	}, body))

	main := expandedMain(t, collab)
	for _, s := range main.Execution.Steps {
		if len(s.Parallel) == 2 && s.Parallel[0].DeptID == s.Parallel[1].DeptID {
			t.Errorf("both parallel branches are department %q", s.Parallel[0].DeptID)
		}
	}
	assertRunOrder(t, runOrder(t, collab), "A", "B", "C", "D")
}

// TestCompileForPublish_BoundaryToAnUnvisitedTask_RunsItOnlyWhenItFires pins
// that the task a boundary moves to is not on the normal path.
func TestCompileForPublish_BoundaryToAnUnvisitedTask_RunsItOnlyWhenItFires(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + userTaskXML("C") +
		"    <bpmn:boundaryEvent id=\"BE\" attachedToRef=\"A\">" + timerP1D + "</bpmn:boundaryEvent>\n" + userTaskXML("Esc") +
		"    <bpmn:endEvent id=\"E\"/>\n    <bpmn:endEvent id=\"E2\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "C") + flowXML("F3", "C", "E") + flowXML("F4", "BE", "Esc") + flowXML("F5", "Esc", "E2")
	collab := compileForPublish(t, flowDocument([]flowLane{
		{"Engineering", engDeptID, []string{"S", "A", "C", "E", "BE"}},
		{"Ops", opsDeptID, []string{"Esc", "E2"}},
	}, body))

	assertRunOrder(t, runOrder(t, collab), "A", "C")
	timer := stageNamed(t, expandedMain(t, collab), "A").BoundaryTimer
	if timer == nil || timer.TargetDept == "" || timer.Terminates {
		t.Fatalf("A's timer = %+v, want a move to Esc's department", timer)
	}
	esc := departmentNamed(t, expandedMain(t, collab), timer.TargetDept)
	if len(esc.Stages) != 1 || esc.Stages[0].NodeID != "Esc" {
		t.Errorf("the timer's target department runs %+v, want Esc alone", esc.Stages)
	}
}

func TestValidate_LaneNameWithAKeySeparator_IsInvalidLaneName(t *testing.T) {
	for _, name := range []string{"Design/QA", "Ops::East", "Ops~2"} {
		t.Run(name, func(t *testing.T) {
			body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + "    <bpmn:endEvent id=\"E\"/>\n" + flowXML("F1", "S", "A") + flowXML("F2", "A", "E")
			errs := validateBlocking(t, flowDocument([]flowLane{{name, engDeptID, []string{"S", "A", "E"}}}, body))
			if findCode(errs, domain.BPMNErrInvalidLaneName) == nil {
				t.Errorf("issues = %+v, want INVALID_LANE_NAME", errs)
			}
		})
	}
}

func stageCount(p *dsl.CompiledPlan, nodeID string) int {
	n := 0
	for _, d := range p.Departments {
		for _, s := range d.Stages {
			if s.NodeID == nodeID {
				n++
			}
		}
	}
	return n
}

// TestCompileForPublish_BoundaryPathRejoiningTheMainPath_KeepsTheMainTask pins
// that a boundary's path stops where it meets a task the main path holds:
// that task keeps its one stage, and a revert to it names its segment.
func TestCompileForPublish_BoundaryPathRejoiningTheMainPath_KeepsTheMainTask(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + userTaskXML("B") + "    <bpmn:exclusiveGateway id=\"X\"/>\n" +
		"    <bpmn:boundaryEvent id=\"BE\" attachedToRef=\"A\">" + timerP1D + "</bpmn:boundaryEvent>\n" + userTaskXML("Esc") + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "B") + flowXML("F3", "B", "X") + conditionalFlowXML("F4", "X", "B", "=r") + flowXML("F5", "X", "E") +
		flowXML("F6", "BE", "Esc") + flowXML("F7", "Esc", "B")
	main := expandedMain(t, compileForPublish(t, flowDocument([]flowLane{
		{"Engineering", engDeptID, []string{"S", "A", "X", "E", "BE"}},
		{"Ops", opsDeptID, []string{"B", "Esc"}},
	}, body)))

	if n := stageCount(main, "B"); n != 1 {
		t.Errorf("B has %d stages, want the main path's one", n)
	}
	assertMovesTo(t, main, stageNamed(t, main, "A").BoundaryTimer.TargetDept, "Ops", "Esc")
	for _, s := range main.Execution.Steps {
		for _, b := range s.Exclusive {
			if b.RevertToNodeID == "B" {
				assertMovesTo(t, main, b.RevertToDept, "Ops", "B")
			}
		}
	}
}

// TestCompileForPublish_BoundaryToATaskTwoBranchesReach_RunsFromThatTask
// pins that a boundary's target starts its department, so the boundary never
// runs tasks before it, a sibling branch's among them.
func TestCompileForPublish_BoundaryToATaskTwoBranchesReach_RunsFromThatTask(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n    <bpmn:exclusiveGateway id=\"X1\"/>\n" + userTaskXML("A") + userTaskXML("B") + userTaskXML("C") + userTaskXML("M") +
		"    <bpmn:exclusiveGateway id=\"J\"/>\n" + userTaskXML("R") + "    <bpmn:boundaryEvent id=\"BE\" attachedToRef=\"R\">" + timerP1D + "</bpmn:boundaryEvent>\n    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "X1") + conditionalFlowXML("F2", "X1", "A", "=a") + conditionalFlowXML("F3", "X1", "B", "=b") + flowXML("F4", "X1", "C") +
		flowXML("F5", "A", "M") + flowXML("F6", "B", "M") + flowXML("F7", "M", "J") + flowXML("F8", "C", "J") + flowXML("F9", "J", "R") + flowXML("F10", "R", "E") + flowXML("F11", "BE", "M")
	main := expandedMain(t, compileForPublish(t, flowDocument([]flowLane{
		{"Engineering", engDeptID, []string{"S", "X1", "A", "J", "R", "E", "BE"}},
		{"Ops", opsDeptID, []string{"B", "C", "M"}},
	}, body)))

	assertMovesTo(t, main, stageNamed(t, main, "R").BoundaryTimer.TargetDept, "Ops", "M")
}

// TestCompileForPublish_ParallelBranchLedByAGateway_NamesItsFirstDepartment
// pins that every branch names the first department its steps run.
func TestCompileForPublish_ParallelBranchLedByAGateway_NamesItsFirstDepartment(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + "    <bpmn:parallelGateway id=\"P\"/>\n    <bpmn:exclusiveGateway id=\"X\"/>\n" +
		userTaskXML("T1") + userTaskXML("T2") + "    <bpmn:exclusiveGateway id=\"J2\"/>\n" + userTaskXML("W") + "    <bpmn:parallelGateway id=\"J1\"/>\n    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "P") + flowXML("F3", "P", "X") + flowXML("F4", "P", "W") +
		conditionalFlowXML("F5", "X", "T1", "=x") + flowXML("F6", "X", "T2") + flowXML("F7", "T1", "J2") + flowXML("F8", "T2", "J2") +
		flowXML("F9", "J2", "J1") + flowXML("F10", "W", "J1") + flowXML("F11", "J1", "E")
	main := expandedMain(t, compileForPublish(t, flowDocument([]flowLane{
		{"Engineering", engDeptID, []string{"S", "A", "P", "X", "J2", "J1", "E", "W"}},
		{"Ops", opsDeptID, []string{"T1", "T2"}},
	}, body)))

	for _, s := range main.Execution.Steps {
		for _, b := range s.Parallel {
			if b.DeptID == "" {
				t.Errorf("parallel branch %+v names no department", b)
			}
		}
	}
}

// TestCompile_ChainedExclusiveSplitsWithoutAJoin_StopAtTheTaskLimit pins that
// compile work is bounded: splits whose branches merge only partly compile
// the merged tail once per branch, so a long chain is refused at the task
// limit rather than compiled for ever.
func TestCompile_ChainedExclusiveSplitsWithoutAJoin_StopAtTheTaskLimit(t *testing.T) {
	const blocks = 30
	var body strings.Builder
	var eng []string
	body.WriteString("    <bpmn:startEvent id=\"S\"/>\n")
	prev := "S"
	for i := range blocks {
		g, ta, tb, k, m, end := fmt.Sprintf("G%d", i), fmt.Sprintf("Ta%d", i), fmt.Sprintf("Tb%d", i), fmt.Sprintf("K%d", i), fmt.Sprintf("M%d", i), fmt.Sprintf("End%d", i)
		fmt.Fprintf(&body, "    <bpmn:exclusiveGateway id=%q/>\n    <bpmn:exclusiveGateway id=%q/>\n    <bpmn:endEvent id=%q/>\n", g, k, end)
		body.WriteString(userTaskXML(ta) + userTaskXML(tb) + userTaskXML(m))
		body.WriteString(flowXML("Fin"+g, prev, g) + conditionalFlowXML("Fa"+g, g, ta, "=a") + flowXML("Fb"+g, g, tb) + flowXML("Fk"+g, ta, k) +
			conditionalFlowXML("Fm"+g, k, m, "=m") + flowXML("Fe"+g, k, end) + flowXML("Fbm"+g, tb, m))
		eng = append(eng, g, ta, tb, k, m, end)
		prev = m
	}
	body.WriteString("    <bpmn:endEvent id=\"E\"/>\n" + flowXML("Fend", prev, "E"))
	bpmnXML := flowDocument([]flowLane{{"Engineering", engDeptID, append([]string{"S", "E"}, eng...)}}, body.String())

	done := make(chan error, 1)
	go func() {
		_, err := bpmn_compiler.New().CompileForPublish(context.Background(), bpmnXML)
		done <- err
	}()
	select {
	case err := <-done:
		var vf *domain.ValidationFailedError
		if !errors.As(err, &vf) || findCode(vf.Errors, domain.BPMNErrTaskLimitExceeded) == nil {
			t.Errorf("CompileForPublish = %v, want TASK_LIMIT_EXCEEDED", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("CompileForPublish did not finish: compile work is unbounded")
	}
}

// TestCompile_ChainedGatewaysWithoutTasks_StopAtTheBudget pins that compile
// work is bounded even when the region compiled once per branch holds no task.
func TestCompile_ChainedGatewaysWithoutTasks_StopAtTheBudget(t *testing.T) {
	const blocks = 30
	var body strings.Builder
	eng := []string{"S", "A", "E"}
	body.WriteString("    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + "    <bpmn:endEvent id=\"E\"/>\n" + flowXML("F0", "S", "A"))
	prev := "A"
	for i := range blocks {
		g, k, m, end := fmt.Sprintf("G%d", i), fmt.Sprintf("K%d", i), fmt.Sprintf("M%d", i), fmt.Sprintf("End%d", i)
		fmt.Fprintf(&body, "    <bpmn:exclusiveGateway id=%q/>\n    <bpmn:exclusiveGateway id=%q/>\n    <bpmn:exclusiveGateway id=%q/>\n    <bpmn:endEvent id=%q/>\n", g, k, m, end)
		body.WriteString(flowXML("Fin"+g, prev, g) + conditionalFlowXML("Fk"+g, g, k, "=k") + flowXML("Fm"+g, g, m) +
			conditionalFlowXML("Fkm"+g, k, m, "=m") + flowXML("Fke"+g, k, end))
		eng = append(eng, g, k, m, end)
		prev = m
	}
	body.WriteString(flowXML("Fend", prev, "E"))
	bpmnXML := flowDocument([]flowLane{{"Engineering", engDeptID, eng}}, body.String())

	done := make(chan error, 1)
	go func() {
		_, err := bpmn_compiler.New().CompileForPublish(context.Background(), bpmnXML)
		done <- err
	}()
	select {
	case err := <-done:
		var vf *domain.ValidationFailedError
		if !errors.As(err, &vf) || findCode(vf.Errors, domain.BPMNErrTaskLimitExceeded) == nil {
			t.Errorf("CompileForPublish = %v, want TASK_LIMIT_EXCEEDED", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CompileForPublish did not finish: compile work is unbounded")
	}
}

// TestValidate_BoundaryToASubProcessStartingWithAGateway_IsUnsupported pins
// that such a boundary is refused: it would run one branch of the
// subprocess's gateway whatever its condition.
func TestValidate_BoundaryToASubProcessStartingWithAGateway_IsUnsupported(t *testing.T) {
	sub := "    <bpmn:subProcess id=\"SP\"><bpmn:startEvent id=\"SP_S\"/>\n    <bpmn:exclusiveGateway id=\"SP_X\"/>\n" + userTaskXML("T1") + userTaskXML("T2") +
		"    <bpmn:endEvent id=\"SP_E1\"/>\n    <bpmn:endEvent id=\"SP_E2\"/>\n" + flowXML("SP_F1", "SP_S", "SP_X") + conditionalFlowXML("SP_F2", "SP_X", "T1", "=x") +
		flowXML("SP_F3", "SP_X", "T2") + flowXML("SP_F4", "T1", "SP_E1") + flowXML("SP_F5", "T2", "SP_E2") + "    </bpmn:subProcess>\n"
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + "    <bpmn:boundaryEvent id=\"BE\" attachedToRef=\"A\">" + timerP1D + "</bpmn:boundaryEvent>\n" +
		sub + "    <bpmn:endEvent id=\"E\"/>\n    <bpmn:endEvent id=\"E2\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "E") + flowXML("F3", "BE", "SP") + flowXML("F4", "SP", "E2")
	errs := validateBlocking(t, flowDocument([]flowLane{{"Engineering", engDeptID, []string{"S", "A", "E", "BE", "SP", "E2"}}}, body))
	if findCode(errs, domain.BPMNErrUnsupportedBoundaryTarget) == nil {
		t.Errorf("issues = %+v, want UNSUPPORTED_BOUNDARY_TARGET", errs)
	}
}

// TestCompileForPublish_RevertToATaskTwoBranchesReach_NamesItsOwnBranchsCopy
// pins that a revert names the segment holding its target on the revert's
// own path, so the history it pops matches the task that ran.
func TestCompileForPublish_RevertToATaskTwoBranchesReach_NamesItsOwnBranchsCopy(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n    <bpmn:exclusiveGateway id=\"X1\"/>\n" + userTaskXML("A") + "    <bpmn:exclusiveGateway id=\"K\"/>\n" + userTaskXML("B") + userTaskXML("M") +
		userTaskXML("R") + "    <bpmn:exclusiveGateway id=\"X2\"/>\n    <bpmn:endEvent id=\"E\"/>\n    <bpmn:endEvent id=\"EK\"/>\n" +
		flowXML("F1", "S", "X1") + conditionalFlowXML("F2", "X1", "A", "=a") + flowXML("F3", "X1", "B") + flowXML("F4", "A", "K") +
		conditionalFlowXML("F5", "K", "M", "=m") + flowXML("F6", "K", "EK") + flowXML("F7", "B", "M") + flowXML("F8", "M", "R") + flowXML("F9", "R", "X2") +
		conditionalFlowXML("F10", "X2", "M", "=r") + flowXML("F11", "X2", "E")
	main := expandedMain(t, compileForPublish(t, flowDocument([]flowLane{
		{"Engineering", engDeptID, []string{"S", "X1", "A", "K", "R", "X2", "E", "EK"}},
		{"Ops", opsDeptID, []string{"B", "M"}},
	}, body)))

	var reverts []dsl.ExclusiveBranch
	var walk func([]dsl.ExecutionStep, []string)
	walk = func(steps []dsl.ExecutionStep, path []string) {
		for _, s := range steps {
			path = append(path, s.Sequential...)
			for _, b := range s.Exclusive {
				if b.RevertToNodeID == "M" {
					if !slices.Contains(path, b.RevertToDept) {
						t.Errorf("revert to M names %q, which its path %v never runs", b.RevertToDept, path)
					}
					reverts = append(reverts, b)
				}
				walk(b.Steps, slices.Clone(path))
			}
		}
	}
	walk(main.Execution.Steps, nil)
	if len(reverts) != 2 {
		t.Fatalf("found %d reverts to M, want one in each branch", len(reverts))
	}
}
