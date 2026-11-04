package bpmn_compiler_test

import (
	"fmt"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// messagingModule is a library module document that declares its own message
// and error: a receive task waits on Msg_ping, the review task has a message
// boundary on Msg_withdraw (an id the caller also uses, with another name),
// and a subProcess has an error boundary on Err_mod.
func messagingModule(id string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="Def_M" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="Msg_ping" name="module-ping"/>
  <bpmn:message id="Msg_withdraw" name="module-withdraw"/>
  <bpmn:error id="Err_mod" errorCode="MODULE_FAILED"/>
  <bpmn:process id=%[1]q isExecutable="true">
    <bpmn:startEvent id="M_Start"/>
    <bpmn:receiveTask id="M_Wait" messageRef="Msg_ping"><bpmn:extensionElements><zeebe:taskDefinition type="receive"/><zeebe:assignmentDefinition candidateGroups="preparator"/></bpmn:extensionElements></bpmn:receiveTask>
%[2]s    <bpmn:boundaryEvent id="M_BMsg" attachedToRef="M_Review"><bpmn:messageEventDefinition messageRef="Msg_withdraw"/></bpmn:boundaryEvent>
    <bpmn:subProcess id="M_Sub">
      <bpmn:startEvent id="MS_Start"/>
      <bpmn:userTask id="MS_Task"><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparator" candidateUsers=%[3]q/></bpmn:extensionElements></bpmn:userTask>
      <bpmn:endEvent id="MS_End"/>
      <bpmn:sequenceFlow id="MS_F1" sourceRef="MS_Start" targetRef="MS_Task"/>
      <bpmn:sequenceFlow id="MS_F2" sourceRef="MS_Task" targetRef="MS_End"/>
    </bpmn:subProcess>
    <bpmn:boundaryEvent id="M_BErr" attachedToRef="M_Sub"><bpmn:errorEventDefinition errorRef="Err_mod"/></bpmn:boundaryEvent>
    <bpmn:endEvent id="M_End"/>
    <bpmn:endEvent id="M_EndMsg"/>
    <bpmn:endEvent id="M_EndErr"/>
%[4]s  </bpmn:process>
</bpmn:definitions>`, id, userTaskXML("M_Review"), validCandidateUser,
		flowXML("M_F1", "M_Start", "M_Wait")+flowXML("M_F2", "M_Wait", "M_Review")+flowXML("M_F3", "M_Review", "M_Sub")+
			flowXML("M_F4", "M_Sub", "M_End")+flowXML("M_F5", "M_BMsg", "M_EndMsg")+flowXML("M_F6", "M_BErr", "M_EndErr"))
}

// TestBundleModules_ModuleKeepsItsOwnMessagesAndErrors pins that a library
// module's message and error definitions come with it into the workflow, and
// that a module's message id never takes the caller's definition of that id.
func TestBundleModules_ModuleKeepsItsOwnMessagesAndErrors(t *testing.T) {
	main := pinned(mainWithCalls([]callSite{
		{ID: "CA_1", Lane: "Engineering", Calls: "Process_Msg", Boundaries: []string{"message"}},
	}), "Process_Msg", "1")
	bundled, err := bpmn_compiler.New().BundleModules(main, []domain.ResolvedModule{
		{ProcessID: "Process_Msg", VersionNumber: 1, PlanName: "Process_Msg@v1", BPMNXML: messagingModule("Process_Msg")},
	})
	if err != nil {
		t.Fatalf("BundleModules: %v", err)
	}
	collab := compileForPublish(t, bundled)

	module := planNamed(t, collab, "Process_Msg@v1")
	if got := stageNamed(t, module, "M_Wait").Extras["message"]; got != "module-ping" {
		t.Errorf("M_Wait waits on %q, want the module's own module-ping", got)
	}
	if got := stageNamed(t, module, "M_Review").BoundaryMessage; got == nil || got.MessageName != "module-withdraw" {
		t.Errorf("M_Review's message boundary = %+v, want the module's own module-withdraw", got)
	}
	var sub *dsl.SubWorkflowStep
	for _, s := range module.Execution.Steps {
		if s.SubWorkflow != nil {
			sub = s.SubWorkflow
		}
	}
	if sub == nil || len(sub.ErrorPaths) != 1 || sub.ErrorPaths[0].ErrorCode != "MODULE_FAILED" {
		t.Errorf("M_Sub = %+v, want an error path on MODULE_FAILED", sub)
	}

	call := callSteps(planNamed(t, collab, collab.MainPlan))[0]
	if len(call.MessagePaths) != 1 || call.MessagePaths[0].MessageName != "withdraw" {
		t.Errorf("the call's own message boundary = %+v, want the workflow's withdraw", call.MessagePaths)
	}
}
