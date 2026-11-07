package service_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const w3aCallSitesBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_CallSites" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:message id="Msg_halt" name="halt"/>
  <bpmn:process id="Process_CallSites" name="Call sites" isExecutable="true">
    <bpmn:laneSet id="CS_LaneSet">
      <bpmn:lane id="Lane_Author" name="Author">
        <bpmn:flowNodeRef>CS_Start</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CS_Route</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CS_ExcCall</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CS_Plain</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CS_Merge</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CS_Timed</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CS_LateTask</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CS_TimerCall</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CS_EndLate</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CS_Awaited</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CS_HaltTask</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CS_MsgCall</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CS_EndHalt</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CS_End</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="CS_Start"/>
    <bpmn:exclusiveGateway id="CS_Route"/>
    <bpmn:callActivity id="CS_ExcCall" name="Check on the first branch">
      <bpmn:extensionElements><zeebe:calledElement processId="Process_Check"/></bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:userTask id="CS_Plain" name="Plain">
      <bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparator"/></bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:exclusiveGateway id="CS_Merge"/>
    <bpmn:userTask id="CS_Timed" name="Timed">
      <bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparator"/></bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:boundaryEvent id="CS_Late" attachedToRef="CS_Timed"><bpmn:timerEventDefinition id="CS_TED"><bpmn:timeDuration>P1D</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:boundaryEvent>
    <bpmn:userTask id="CS_LateTask" name="Handle late">
      <bpmn:extensionElements><zeebe:taskDefinition type="approve"/><zeebe:assignmentDefinition candidateGroups="approver"/></bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:callActivity id="CS_TimerCall" name="Check when late">
      <bpmn:extensionElements><zeebe:calledElement processId="Process_Check"/></bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="CS_EndLate"/>
    <bpmn:userTask id="CS_Awaited" name="Awaited">
      <bpmn:extensionElements><zeebe:taskDefinition type="approve"/><zeebe:assignmentDefinition candidateGroups="approver"/></bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:boundaryEvent id="CS_Halt" attachedToRef="CS_Awaited"><bpmn:messageEventDefinition id="CS_MED" messageRef="Msg_halt"/></bpmn:boundaryEvent>
    <bpmn:userTask id="CS_HaltTask" name="Handle halt">
      <bpmn:extensionElements><zeebe:taskDefinition type="approve"/><zeebe:assignmentDefinition candidateGroups="approver"/></bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:callActivity id="CS_MsgCall" name="Check when halted">
      <bpmn:extensionElements><zeebe:calledElement processId="Process_Check"/></bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="CS_EndHalt"/>
    <bpmn:endEvent id="CS_End"/>
    <bpmn:sequenceFlow id="CS_F1" sourceRef="CS_Start" targetRef="CS_Route"/>
    <bpmn:sequenceFlow id="CS_F2" sourceRef="CS_Route" targetRef="CS_ExcCall"><bpmn:conditionExpression>= pick = "check"</bpmn:conditionExpression></bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="CS_F3" sourceRef="CS_Route" targetRef="CS_Plain"/>
    <bpmn:sequenceFlow id="CS_F4" sourceRef="CS_ExcCall" targetRef="CS_Merge"/>
    <bpmn:sequenceFlow id="CS_F5" sourceRef="CS_Plain" targetRef="CS_Merge"/>
    <bpmn:sequenceFlow id="CS_F6" sourceRef="CS_Merge" targetRef="CS_Timed"/>
    <bpmn:sequenceFlow id="CS_F7" sourceRef="CS_Timed" targetRef="CS_Awaited"/>
    <bpmn:sequenceFlow id="CS_F8" sourceRef="CS_Awaited" targetRef="CS_End"/>
    <bpmn:sequenceFlow id="CS_F9" sourceRef="CS_Late" targetRef="CS_LateTask"/>
    <bpmn:sequenceFlow id="CS_F9b" sourceRef="CS_LateTask" targetRef="CS_TimerCall"/>
    <bpmn:sequenceFlow id="CS_F10" sourceRef="CS_TimerCall" targetRef="CS_EndLate"/>
    <bpmn:sequenceFlow id="CS_F11" sourceRef="CS_Halt" targetRef="CS_HaltTask"/>
    <bpmn:sequenceFlow id="CS_F11b" sourceRef="CS_HaltTask" targetRef="CS_MsgCall"/>
    <bpmn:sequenceFlow id="CS_F12" sourceRef="CS_MsgCall" targetRef="CS_EndHalt"/>
  </bpmn:process>
</bpmn:definitions>`

func TestModuleContract_CallsOnAnExclusiveBranchAndOnTimerAndMessageBoundaryPathsAreListed(t *testing.T) {
	v := libraryVersion(uuid.New(), "Process_CallSites", 1, w3aCallSitesBPMN)
	svc, tenantID := contractService(t, "tenant", v, checkLibrary)

	got, err := svc.Contract(context.Background(), tenantID, v.ModuleID, v.ID)
	require.NoError(t, err)

	var keys []string
	for _, task := range got.Tasks {
		keys = append(keys, task.AssigneeKey)
	}
	slices.Sort(keys)
	assert.Equal(t, []string{
		"CS_Awaited",
		"CS_ExcCall::Check_Task",
		"CS_HaltTask",
		"CS_LateTask",
		"CS_MsgCall::Check_Task",
		"CS_Plain",
		"CS_Timed",
		"CS_TimerCall::Check_Task",
	}, keys, "each call on an exclusive branch, a timer path and a message path lists the called module's task under its own call")
}
