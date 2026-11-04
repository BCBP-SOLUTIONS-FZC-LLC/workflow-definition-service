package bpmn_compiler_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// externalPool is an ignored pool, "Vendor": it sends a bid to the main
// pool's Receive_Bid and receives the main pool's award from Send_Award.
const externalPool = `  <bpmn:process id="Process_Vendor" name="Vendor" isExecutable="true">
    <bpmn:extensionElements><zeebe:properties><zeebe:property name="ignore" value="true"/></zeebe:properties></bpmn:extensionElements>
    <bpmn:startEvent id="V_S"/>
    <bpmn:sendTask id="V_Send" name="Send bid"/>
    <bpmn:receiveTask id="V_Wait" name="Wait for award"/>
    <bpmn:endEvent id="V_E"/>
    <bpmn:sequenceFlow id="V_F1" sourceRef="V_S" targetRef="V_Send"/>
    <bpmn:sequenceFlow id="V_F2" sourceRef="V_Send" targetRef="V_Wait"/>
    <bpmn:sequenceFlow id="V_F3" sourceRef="V_Wait" targetRef="V_E"/>
  </bpmn:process>`

// poolsDocument wraps a main process, built as flowDocument builds one, in a
// collaboration with the Vendor pool and the given message flows.
func poolsDocument(lanes []flowLane, body, messageFlows string, procs ...string) string {
	doc := flowDocument(lanes, body, append([]string{externalPool}, procs...)...)
	collab := `  <bpmn:collaboration id="Collab">
    <bpmn:participant id="P_Main" name="Buyer" processRef="Process_Main"/>
    <bpmn:participant id="P_Vendor" name="Vendor" processRef="Process_Vendor"/>
` + messageFlows + `  </bpmn:collaboration>
  <bpmn:message id="Msg_bid" name="bid-received"/>
`
	return strings.Replace(doc, "  <bpmn:process id=\"Process_Main\"", collab+"  <bpmn:process id=\"Process_Main\"", 1)
}

// tenderBody is S → Prepare (Ops) → Send_Invite → Receive_Bid → Award → E,
// with Receive_Bid waiting for the Vendor's bid and Send_Invite naming no
// message of its own.
func tenderBody(award string) string {
	return "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("Prepare") +
		"    <bpmn:sendTask id=\"Send_Invite\" name=\"Invite\"/>\n    <bpmn:receiveTask id=\"Receive_Bid\" name=\"Receive bid\"/>\n" +
		award + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "Prepare") + flowXML("F2", "Prepare", "Send_Invite") + flowXML("F3", "Send_Invite", "Receive_Bid") +
		flowXML("F4", "Receive_Bid", "Award") + flowXML("F5", "Award", "E")
}

const tenderMessageFlows = `    <bpmn:messageFlow id="MF1" sourceRef="Send_Invite" targetRef="V_S"/>
    <bpmn:messageFlow id="MF2" sourceRef="V_Send" targetRef="Receive_Bid" messageRef="Msg_bid"/>
`

func tenderDocument(award string) string {
	return poolsDocument([]flowLane{{"Ops", opsDeptID, []string{"S", "Prepare", "Send_Invite", "Receive_Bid", "Award", "E"}}}, tenderBody(award), tenderMessageFlows)
}

func allSteps(steps []dsl.ExecutionStep, visit func(dsl.ExecutionStep)) {
	for _, s := range steps {
		visit(s)
		for _, b := range s.Parallel {
			allSteps(b.Steps, visit)
		}
		for _, b := range s.Exclusive {
			allSteps(b.Steps, visit)
		}
		if s.SubWorkflow != nil {
			allSteps(s.SubWorkflow.Plan.Steps, visit)
		}
	}
}

// TestCompileForPublish_Collaboration_RunsItsOnePool pins that an ignored
// pool produces nothing: the published plan is the main pool's alone, its
// receive task waits for the message the external pool sends it, and no
// step stands for the external pool.
func TestCompileForPublish_Collaboration_RunsItsOnePool(t *testing.T) {
	collab := compileForPublish(t, tenderDocument(userTaskXML("Award")))

	if len(collab.Plans) != 1 || collab.MainPlan != "Main" {
		var names []string
		for _, p := range collab.Plans {
			names = append(names, p.Name)
		}
		t.Fatalf("plans = %v, main %q; want the main pool's plan alone", names, collab.MainPlan)
	}
	main := expandedMain(t, collab)
	assertRunOrder(t, runOrder(t, collab), "Prepare", "Send_Invite", "Receive_Bid", "Award")
	if got := stageNamed(t, main, "Receive_Bid").Extras["message"]; got != "bid-received" {
		t.Errorf("Receive_Bid waits for %q, want the vendor's bid-received", got)
	}
	allSteps(main.Execution.Steps, func(s dsl.ExecutionStep) {
		if len(s.Sequential) == 0 && len(s.Parallel) == 0 && len(s.Exclusive) == 0 && s.SubWorkflow == nil {
			t.Errorf("step %+v stands for no work of the main pool", s)
		}
	})
}

// TestValidateForPublish_Collaboration_JudgesAsPublish pins that /validate
// applies publish's own rules to a collaboration's pool, as it does to a
// single process: here, a task with no default user.
func TestValidateForPublish_Collaboration_JudgesAsPublish(t *testing.T) {
	award := `    <bpmn:userTask id="Award"><bpmn:extensionElements><zeebe:taskDefinition type="approve"/><zeebe:assignmentDefinition candidateGroups="reviewer"/></bpmn:extensionElements></bpmn:userTask>
`
	errs, err := bpmn_compiler.New().ValidateForPublish(context.Background(), tenderDocument(award))
	if err != nil {
		t.Fatalf("ValidateForPublish: %v", err)
	}
	if e := findCode(errs, domain.BPMNErrCandidateUsersEmpty); e == nil || e.Severity == domain.SeverityWarning {
		t.Errorf("issues = %+v, want CANDIDATE_USERS_EMPTY as an error on Award", errs)
	}
}

// TestCompileForPublish_CollaborationWithNoRunnablePool_IsRefused pins a
// clear error when every pool is ignored.
func TestCompileForPublish_CollaborationWithNoRunnablePool_IsRefused(t *testing.T) {
	doc := strings.Replace(tenderDocument(userTaskXML("Award")), `<bpmn:process id="Process_Main" name="Main" isExecutable="true">`,
		`<bpmn:process id="Process_Main" name="Main" isExecutable="true"><bpmn:extensionElements><zeebe:properties><zeebe:property name="ignore" value="true"/></zeebe:properties></bpmn:extensionElements>`, 1)

	_, err := bpmn_compiler.New().CompileForPublish(context.Background(), doc)
	var vf *domain.ValidationFailedError
	if !errors.As(err, &vf) || findCode(vf.Errors, domain.BPMNErrNoRunnableProcess) == nil {
		t.Errorf("CompileForPublish = %v, want NO_RUNNABLE_PROCESS", err)
	}
}

// TestCompileForPublish_PoolWithoutAStartEvent_StartsAtItsFirstNode pins
// that a pool entered by a message, as in the Tender examples, starts at its
// one node with no incoming flow.
func TestCompileForPublish_PoolWithoutAStartEvent_StartsAtItsFirstNode(t *testing.T) {
	body := userTaskXML("Prepare") + userTaskXML("Award") + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "Prepare", "Award") + flowXML("F2", "Award", "E")
	doc := poolsDocument([]flowLane{{"Ops", opsDeptID, []string{"Prepare", "Award", "E"}}}, body,
		"    <bpmn:messageFlow id=\"MF1\" sourceRef=\"V_Send\" targetRef=\"Prepare\" messageRef=\"Msg_bid\"/>\n")

	assertRunOrder(t, runOrder(t, compileForPublish(t, doc)), "Prepare", "Award")
}

// TestCompileForPublish_Bnb_RunsTheContractorPoolAndItsModule pins the real
// example: the consultant pool, marked ignore, produces nothing, while the
// process the contractor pool calls runs as its module even though it is a
// non-executable pool of its own.
func TestCompileForPublish_Bnb_RunsTheContractorPoolAndItsModule(t *testing.T) {
	collab := compileForPublish(t, mustReadBPMN(t, "bnb.bpmn"))

	if collab.MainPlan != "Bid-No-Bid Review" {
		t.Errorf("main plan = %q, want the contractor pool, Bid-No-Bid Review", collab.MainPlan)
	}
	planNamed(t, collab, "Docs_preparation_ex")
	for _, p := range collab.Plans {
		if p.Name == "Issue RFQ" || p.Name == "Process_consultant" {
			t.Errorf("the ignored consultant pool compiled to plan %q", p.Name)
		}
		if strings.Contains(fmt.Sprint(p.Departments), "/") {
			t.Errorf("plan %q has pool-qualified departments %+v", p.Name, p.Departments)
		}
	}
	main := expandedMain(t, collab)
	var moduleTasks int
	for _, d := range main.Departments {
		if strings.HasPrefix(d.ID, "Activity_0tcolgg::") {
			moduleTasks += len(d.Stages)
		}
	}
	if moduleTasks == 0 {
		t.Errorf("the called module's tasks are not in the expanded plan: %+v", main.Departments)
	}
}

// TestCompileForPublish_NonExecutablePoolNothingCalls_ProducesNothing pins
// that a pool drawn for context, not executable and not called, is external.
func TestCompileForPublish_NonExecutablePoolNothingCalls_ProducesNothing(t *testing.T) {
	doc := strings.Replace(tenderDocument(userTaskXML("Award")),
		`<bpmn:process id="Process_Vendor" name="Vendor" isExecutable="true">
    <bpmn:extensionElements><zeebe:properties><zeebe:property name="ignore" value="true"/></zeebe:properties></bpmn:extensionElements>`,
		`<bpmn:process id="Process_Vendor" name="Vendor" isExecutable="false">`, 1)

	collab := compileForPublish(t, doc)
	if len(collab.Plans) != 1 || collab.MainPlan != "Main" {
		t.Errorf("plans = %d, main %q; want the main pool alone", len(collab.Plans), collab.MainPlan)
	}
}

// TestCompileForPublish_CalledProcessMarkedIgnore_IsRefused pins that a
// process cannot be both an external pool and a module the workflow runs.
func TestCompileForPublish_CalledProcessMarkedIgnore_IsRefused(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + callXML("CA", "Process_Vendor") + "    <bpmn:endEvent id=\"E\"/>\n" + flowXML("F1", "S", "CA") + flowXML("F2", "CA", "E")
	doc := poolsDocument([]flowLane{{"Ops", opsDeptID, []string{"S", "CA", "E"}}}, body, "")

	_, err := bpmn_compiler.New().CompileForPublish(context.Background(), doc)
	var vf *domain.ValidationFailedError
	if !errors.As(err, &vf) || findCode(vf.Errors, domain.BPMNErrUnresolvedCalledElement) == nil {
		t.Errorf("CompileForPublish = %v, want UNRESOLVED_CALLED_ELEMENT on a call to an ignored pool", err)
	}
}

// ignoreLane marks the lane named name in a flowDocument as an outside
// party's.
func ignoreLane(doc, name string) string {
	return strings.Replace(doc, fmt.Sprintf(`name=%q><bpmn:extensionElements><zeebe:properties>`, name),
		fmt.Sprintf(`name=%q><bpmn:extensionElements><zeebe:properties><zeebe:property name="ignore" value="true"/>`, name), 1)
}

const vendorWork = "    <bpmn:userTask id=\"Vendor_Work\" name=\"Vendor works\"/>\n"

// vendorLaneDocument is S → Prepare (Ops) → Vendor_Work (Vendor) → Award
// (Ops) → E, with the Vendor lane marked ignore. vendor is Vendor_Work's
// element; extra and its nodes are drawn in the Vendor lane.
func vendorLaneDocument(vendor, extra string, extraNodes []string, procs ...string) string {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("Prepare") + vendor + userTaskXML("Award") + "    <bpmn:endEvent id=\"E\"/>\n" + extra +
		flowXML("F1", "S", "Prepare") + flowXML("F2", "Prepare", "Vendor_Work") + flowXML("F3", "Vendor_Work", "Award") + flowXML("F4", "Award", "E")
	return ignoreLane(flowDocument([]flowLane{
		{"Ops", opsDeptID, []string{"S", "Prepare", "Award", "E"}},
		{"Vendor", engDeptID, append([]string{"Vendor_Work"}, extraNodes...)},
	}, body, procs...), "Vendor")
}

// TestCompileForPublish_IgnoredLane_SkipsItsTasks pins that a task in an
// ignored lane is an outside party's: it compiles to nothing, needs no task
// type or user, and the flow runs on to the next internal task.
func TestCompileForPublish_IgnoredLane_SkipsItsTasks(t *testing.T) {
	doc := vendorLaneDocument(vendorWork, "", nil)

	errs, err := bpmn_compiler.New().ValidateForPublish(context.Background(), doc)
	if err != nil || slices.ContainsFunc(errs, isError) {
		t.Fatalf("ValidateForPublish = %+v, %v; want no errors for a task in an ignored lane", errs, err)
	}
	collab := compileForPublish(t, doc)
	assertRunOrder(t, runOrder(t, collab), "Prepare", "Award")
	for _, d := range expandedMain(t, collab).Departments {
		if d.IAMDepartmentID == engDeptID {
			t.Errorf("the ignored Vendor lane has department %+v", d)
		}
	}
}

func isError(e domain.BPMNValidationError) bool { return e.Severity != domain.SeverityWarning }

// TestCompileForPublish_Bnb_SkipsTheModulesIgnoredLane pins bnb's Docs prep:
// the module runs, its Receiver tasks with it, while its Sender lane, marked
// ignore, contributes no task.
func TestCompileForPublish_Bnb_SkipsTheModulesIgnoredLane(t *testing.T) {
	module := planNamed(t, compileForPublish(t, mustReadBPMN(t, "bnb.bpmn")), "Docs_preparation_ex")

	nodes := map[string]bool{}
	for _, d := range module.Departments {
		for _, s := range d.Stages {
			nodes[s.NodeID] = true
		}
	}
	for _, id := range []string{"Activity_0zz5tcp", "Activity_13swln3", "Activity_00syxjl", "Activity_1l5l53o", "Activity_0fdth99"} {
		if !nodes[id] {
			t.Errorf("Receiver task %s is missing from the module: %v", id, nodes)
		}
	}
	for _, id := range []string{"Activity_08ot7y2", "Activity_14pvn0b"} {
		if nodes[id] {
			t.Errorf("Sender task %s, in the ignored lane, compiled to a stage", id)
		}
	}
}

// TestCompileForPublish_WorkThatCannotBeSkipped_InAnIgnoredLane_IsRefused pins
// that a call, which runs a module, and a boundary, which would wait on a
// task that never runs, cannot sit in an ignored lane.
func TestCompileForPublish_WorkThatCannotBeSkipped_InAnIgnoredLane_IsRefused(t *testing.T) {
	boundary := `    <bpmn:boundaryEvent id="Vendor_Late" attachedToRef="Vendor_Work"><bpmn:timerEventDefinition><bpmn:timeDuration>P1D</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:boundaryEvent>
    <bpmn:endEvent id="E2"/>
` + flowXML("F9", "Vendor_Late", "E2")
	cases := map[string]struct {
		doc, node string
	}{
		"call":     {vendorLaneDocument(callXML("Vendor_Work", "Process_M"), "", nil, moduleWithTask("Process_M", validCandidateUser)), "Vendor_Work"},
		"boundary": {vendorLaneDocument(vendorWork, boundary, []string{"Vendor_Late", "E2"}), "Vendor_Late"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := bpmn_compiler.New().CompileForPublish(context.Background(), tc.doc)
			var vf *domain.ValidationFailedError
			if !errors.As(err, &vf) {
				t.Fatalf("CompileForPublish = %v, want a validation failure", err)
			}
			if e := findCode(vf.Errors, domain.BPMNErrIgnoredLaneWork); e == nil || e.NodeID != tc.node {
				t.Errorf("errors = %+v, want IGNORED_LANE_WORK on %s", vf.Errors, tc.node)
			}
		})
	}
}

// refusedWith compiles doc for publish and returns the issue with code, or
// fails the test when publish does not refuse it with that code.
func refusedWith(t *testing.T, doc string, code domain.BPMNErrorCode) domain.BPMNValidationError {
	t.Helper()
	_, err := bpmn_compiler.New().CompileForPublish(context.Background(), doc)
	var vf *domain.ValidationFailedError
	if !errors.As(err, &vf) {
		t.Fatalf("CompileForPublish = %v, want a validation failure with %s", err, code)
	}
	e := findCode(vf.Errors, code)
	if e == nil {
		t.Fatalf("errors = %+v, want %s", vf.Errors, code)
	}
	return *e
}

// TestCompileForPublish_BoundaryPathThroughASkippedTask_IsJudgedPastIt pins
// that a boundary's path is checked as it compiles, past the tasks of an
// ignored lane: a path that reaches a call after a skipped task is refused,
// as a path starting at a call is, and one that reaches a task after a
// skipped send task runs from that task.
func TestCompileForPublish_BoundaryPathThroughASkippedTask_IsJudgedPastIt(t *testing.T) {
	path := func(skipped, after string) string {
		return `    <bpmn:boundaryEvent id="Late" attachedToRef="Prepare">` + timerP1D + `</bpmn:boundaryEvent>
` + skipped + after + "    <bpmn:endEvent id=\"E2\"/>\n" +
			flowXML("F9", "Late", "Vendor_Late") + flowXML("F10", "Vendor_Late", "After") + flowXML("F11", "After", "E2")
	}
	doc := func(extra string, procs ...string) string {
		body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("Prepare") + userTaskXML("Award") + "    <bpmn:endEvent id=\"E\"/>\n" + extra +
			flowXML("F1", "S", "Prepare") + flowXML("F2", "Prepare", "Award") + flowXML("F3", "Award", "E")
		return ignoreLane(flowDocument([]flowLane{
			{"Ops", opsDeptID, []string{"S", "Prepare", "Award", "E", "After", "E2"}},
			{"Vendor", engDeptID, []string{"Vendor_Late"}},
		}, body, procs...), "Vendor")
	}

	t.Run("to a call", func(t *testing.T) {
		e := refusedWith(t, doc(path("    <bpmn:userTask id=\"Vendor_Late\"/>\n", callXML("After", "Process_M")), moduleWithTask("Process_M", validCandidateUser)), domain.BPMNErrUnsupportedBoundaryTarget)
		if e.NodeID != "Late" {
			t.Errorf("UNSUPPORTED_BOUNDARY_TARGET on %q, want the boundary Late", e.NodeID)
		}
	})
	t.Run("to a task", func(t *testing.T) {
		collab := compileForPublish(t, doc(path("    <bpmn:sendTask id=\"Vendor_Late\"/>\n", userTaskXML("After"))))
		timer := stageNamed(t, expandedMain(t, collab), "Prepare").BoundaryTimer
		if timer == nil || timer.Terminates || timer.TargetDept == "" {
			t.Fatalf("Prepare's timer = %+v, want it to run After's department", timer)
		}
		if got := stageNamed(t, expandedMain(t, collab), "After"); got.NodeID != "After" {
			t.Errorf("After did not compile: %+v", got)
		}
	})
}

// loopDocument is S → Prepare → M → Vendor_Work (Vendor, ignored) → after →
// G, with G looping back to target when decision is "again" and going on to
// Award otherwise.
func loopDocument(target, after string, afterNodes ...string) string {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("Prepare") + "    <bpmn:exclusiveGateway id=\"M\"/>\n" + vendorWork + after +
		"    <bpmn:exclusiveGateway id=\"G\"/>\n" + userTaskXML("Award") + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "Prepare") + flowXML("F2", "Prepare", "M") + flowXML("F3", "M", "Vendor_Work") +
		`    <bpmn:sequenceFlow id="F4" sourceRef="G" targetRef="` + target + `"><bpmn:conditionExpression>=decision == "again"</bpmn:conditionExpression></bpmn:sequenceFlow>
` + flowXML("F6", "G", "Award") + flowXML("F7", "Award", "E")
	if after == "" {
		body += flowXML("F5", "Vendor_Work", "G")
	} else {
		body += flowXML("F5", "Vendor_Work", "Review") + flowXML("F8", "Review", "G")
	}
	return ignoreLane(flowDocument([]flowLane{
		{"Ops", opsDeptID, append([]string{"S", "Prepare", "M", "G", "Award", "E"}, afterNodes...)},
		{"Vendor", engDeptID, []string{"Vendor_Work"}},
	}, body), "Vendor")
}

// TestCompileForPublish_LoopThatRunsNothingAgain_IsRefused pins that a loop
// whose way back holds only skipped tasks is refused, whether it returns to
// the skipped task or to a gateway before it: nothing would run again, and
// the revert would go forward instead.
func TestCompileForPublish_LoopThatRunsNothingAgain_IsRefused(t *testing.T) {
	for _, target := range []string{"Vendor_Work", "M"} {
		t.Run(target, func(t *testing.T) {
			if e := refusedWith(t, loopDocument(target, ""), domain.BPMNErrIgnoredLaneWork); e.NodeID != "F4" {
				t.Errorf("IGNORED_LANE_WORK on %q, want the flow F4", e.NodeID)
			}
		})
	}
}

// TestCompileForPublish_LoopThroughASkippedTaskToATask_RevertsToIt pins that
// a loop back through a skipped task to a task of the workflow's own is
// fine: it reverts to that task.
func TestCompileForPublish_LoopThroughASkippedTaskToATask_RevertsToIt(t *testing.T) {
	collab := compileForPublish(t, loopDocument("Vendor_Work", userTaskXML("Review"), "Review"))
	var revert string
	allSteps(expandedMain(t, collab).Execution.Steps, func(s dsl.ExecutionStep) {
		for _, b := range s.Exclusive {
			if b.RevertToNodeID != "" {
				revert = b.RevertToNodeID
			}
		}
	})
	if revert != "Review" {
		t.Errorf("the loop reverts to %q, want Review", revert)
	}
}

// twoLaneModule has open lanes A and B, each with one task.
const twoLaneModule = `  <bpmn:process id="Process_Two" isExecutable="true">
    <bpmn:laneSet id="LS_Two">
      <bpmn:lane id="Two_A" name="A"><bpmn:flowNodeRef>MT1</bpmn:flowNodeRef></bpmn:lane>
      <bpmn:lane id="Two_B" name="B"><bpmn:flowNodeRef>MT2</bpmn:flowNodeRef></bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Two_S"/>
    <bpmn:userTask id="MT1"><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparator" candidateUsers="` + validCandidateUser + `"/></bpmn:extensionElements></bpmn:userTask>
    <bpmn:userTask id="MT2"><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparator" candidateUsers="` + validCandidateUser + `"/></bpmn:extensionElements></bpmn:userTask>
    <bpmn:endEvent id="Two_E"/>
    <bpmn:sequenceFlow id="Two_F1" sourceRef="Two_S" targetRef="MT1"/>
    <bpmn:sequenceFlow id="Two_F2" sourceRef="MT1" targetRef="MT2"/>
    <bpmn:sequenceFlow id="Two_F3" sourceRef="MT2" targetRef="Two_E"/>
  </bpmn:process>`

// TestCompileForPublish_CallBindingAnIgnoredLane_IsRefused pins that a call
// cannot hand a module's lane to an outside party's lane: the module's work
// is the workflow's own.
func TestCompileForPublish_CallBindingAnIgnoredLane_IsRefused(t *testing.T) {
	call := `    <bpmn:callActivity id="CA"><bpmn:extensionElements><zeebe:calledElement processId="Process_Two"/><zeebe:ioMapping><zeebe:input source='{"A":"Ops","B":"Vendor"}' target="Depts"/></zeebe:ioMapping></bpmn:extensionElements></bpmn:callActivity>
`
	body := "    <bpmn:startEvent id=\"S\"/>\n" + call + vendorWork + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "CA") + flowXML("F2", "CA", "Vendor_Work") + flowXML("F3", "Vendor_Work", "E")
	doc := ignoreLane(flowDocument([]flowLane{
		{"Ops", opsDeptID, []string{"S", "CA", "E"}},
		{"Vendor", engDeptID, []string{"Vendor_Work"}},
	}, body, twoLaneModule), "Vendor")

	if e := refusedWith(t, doc, domain.BPMNErrInvalidZeebeProperty); e.NodeID != "CA" {
		t.Errorf("INVALID_ZEEBE_PROPERTY on %q, want the call CA", e.NodeID)
	}
}

// TestCompileForPublish_ConnectorInAnIgnoredLane_IsRefused pins that a
// connector task, automatic work of the workflow's own, cannot be skipped.
func TestCompileForPublish_ConnectorInAnIgnoredLane_IsRefused(t *testing.T) {
	connector := `    <bpmn:serviceTask id="Vendor_Work"><bpmn:extensionElements><zeebe:taskDefinition type="connector:storage"/></bpmn:extensionElements></bpmn:serviceTask>
`
	if e := refusedWith(t, vendorLaneDocument(connector, "", nil), domain.BPMNErrIgnoredLaneWork); e.NodeID != "Vendor_Work" {
		t.Errorf("IGNORED_LANE_WORK on %q, want the connector Vendor_Work", e.NodeID)
	}
}

// TestCompileForPublish_IgnoredPoolCallingTheWorkflow_StillRunsIt pins that
// an outside party's pool does not decide which pool runs: its own calls are
// context, drawn and never made.
func TestCompileForPublish_IgnoredPoolCallingTheWorkflow_StillRunsIt(t *testing.T) {
	doc := strings.Replace(tenderDocument(userTaskXML("Award")), `<bpmn:receiveTask id="V_Wait" name="Wait for award"/>`,
		`<bpmn:callActivity id="V_Wait"><bpmn:extensionElements><zeebe:calledElement processId="Process_Main"/></bpmn:extensionElements></bpmn:callActivity>`, 1)

	if collab := compileForPublish(t, doc); collab.MainPlan != "Main" {
		t.Errorf("main plan = %q, want Main", collab.MainPlan)
	}
}

// TestValidate_DataInputAssociation_IsDiagramOnly pins that a data input
// association, written by bpmn-js with a placeholder property on its task, is
// accepted as a decoration.
func TestValidate_DataInputAssociation_IsDiagramOnly(t *testing.T) {
	task := `    <bpmn:userTask id="Award"><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="reviewer" candidateUsers="` + validCandidateUser + `"/></bpmn:extensionElements>
      <bpmn:documentation>Award the bid</bpmn:documentation>
      <bpmn:property id="Property_1" name="__targetRef_placeholder"/>
      <bpmn:dataInputAssociation id="DIA_1"><bpmn:sourceRef>Store</bpmn:sourceRef><bpmn:targetRef>Property_1</bpmn:targetRef></bpmn:dataInputAssociation>
    </bpmn:userTask>
    <bpmn:dataStoreReference id="Store"/>
`
	errs, err := bpmn_compiler.New().Validate(context.Background(), tenderDocument(task))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if e := findCode(errs, domain.BPMNErrRejectedElement); e != nil {
		t.Errorf("REJECTED_ELEMENT %+v, want the data input association accepted", e)
	}
}

// TestValidate_UnnamedMessageFlow_WarnsOnlyWhereSomethingWaits pins the
// warning for a message flow with no name to the receivers that run: a task
// of the workflow or of a module it calls, never a skipped task.
func TestValidate_UnnamedMessageFlow_WarnsOnlyWhereSomethingWaits(t *testing.T) {
	warned := func(t *testing.T, doc string) bool {
		t.Helper()
		errs, err := bpmn_compiler.New().Validate(context.Background(), doc)
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		e := findCode(errs, domain.BPMNErrMissingMessageDefinition)
		return e != nil && e.NodeID == "MF9"
	}
	t.Run("into a skipped task", func(t *testing.T) {
		body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("Prepare") + "    <bpmn:receiveTask id=\"Vendor_Work\"/>\n" + userTaskXML("Award") + "    <bpmn:endEvent id=\"E\"/>\n" +
			flowXML("F1", "S", "Prepare") + flowXML("F2", "Prepare", "Vendor_Work") + flowXML("F3", "Vendor_Work", "Award") + flowXML("F4", "Award", "E")
		doc := ignoreLane(poolsDocument([]flowLane{
			{"Ops", opsDeptID, []string{"S", "Prepare", "Award", "E"}},
			{"Vendor", engDeptID, []string{"Vendor_Work"}},
		}, body, `    <bpmn:messageFlow id="MF9" sourceRef="V_Send" targetRef="Vendor_Work"/>
`), "Vendor")
		if warned(t, doc) {
			t.Error("a flow into a skipped receive task warned; nothing waits for it")
		}
	})
	t.Run("into a user task", func(t *testing.T) {
		doc := poolsDocument([]flowLane{{"Ops", opsDeptID, []string{"S", "Prepare", "Send_Invite", "Receive_Bid", "Award", "E"}}}, tenderBody(userTaskXML("Award")),
			tenderMessageFlows+`    <bpmn:messageFlow id="MF9" sourceRef="V_Send" targetRef="Award"/>
`)
		if warned(t, doc) {
			t.Error("a flow into a user task warned; nothing waits for it")
		}
	})
	t.Run("into a module", func(t *testing.T) {
		module := `  <bpmn:process id="Process_Wait" isExecutable="false">
    <bpmn:startEvent id="W_S"/>
    <bpmn:receiveTask id="W_Recv"/>
    <bpmn:endEvent id="W_E"/>
    <bpmn:sequenceFlow id="W_F1" sourceRef="W_S" targetRef="W_Recv"/>
    <bpmn:sequenceFlow id="W_F2" sourceRef="W_Recv" targetRef="W_E"/>
  </bpmn:process>`
		body := "    <bpmn:startEvent id=\"S\"/>\n" + callXML("CA", "Process_Wait") + "    <bpmn:endEvent id=\"E\"/>\n" + flowXML("F1", "S", "CA") + flowXML("F2", "CA", "E")
		doc := poolsDocument([]flowLane{{"Ops", opsDeptID, []string{"S", "CA", "E"}}}, body,
			`    <bpmn:messageFlow id="MF9" sourceRef="V_Send" targetRef="W_Recv"/>
`, module)
		if !warned(t, doc) {
			t.Error("a flow with no name into a module's receive task did not warn")
		}
	})
}

// TestCompileForPublish_IgnoredPoolCallingItself_Publishes pins that the call
// checks judge only calls that are made: a loop drawn inside an outside
// party's pool is context.
func TestCompileForPublish_IgnoredPoolCallingItself_Publishes(t *testing.T) {
	doc := strings.Replace(tenderDocument(userTaskXML("Award")), `<bpmn:receiveTask id="V_Wait" name="Wait for award"/>`,
		`<bpmn:callActivity id="V_Wait"><bpmn:extensionElements><zeebe:calledElement processId="Process_Vendor"/></bpmn:extensionElements></bpmn:callActivity>`, 1)

	if collab := compileForPublish(t, doc); collab.MainPlan != "Main" {
		t.Errorf("main plan = %q, want Main", collab.MainPlan)
	}
}
