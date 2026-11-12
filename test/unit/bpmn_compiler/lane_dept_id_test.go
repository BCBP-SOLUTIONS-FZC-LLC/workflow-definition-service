package bpmn_compiler_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func moduleWithALaneThatOnlyHostsASubProcess(deptID string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="Def_L" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_L" isExecutable="true">
    <bpmn:laneSet id="LS_1">
      <bpmn:lane id="Lane_x" name="Design">
        <bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value=%q/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Sub_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="L_Start"/>
    <bpmn:subProcess id="Sub_1">
      <bpmn:startEvent id="LS_Start"/>
      <bpmn:userTask id="LS_Task"><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparator" candidateUsers=%q/></bpmn:extensionElements></bpmn:userTask>
      <bpmn:endEvent id="LS_End"/>
      <bpmn:sequenceFlow id="LS_F1" sourceRef="LS_Start" targetRef="LS_Task"/>
      <bpmn:sequenceFlow id="LS_F2" sourceRef="LS_Task" targetRef="LS_End"/>
    </bpmn:subProcess>
    <bpmn:endEvent id="L_End"/>
    <bpmn:sequenceFlow id="L_F1" sourceRef="L_Start" targetRef="Sub_1"/>
    <bpmn:sequenceFlow id="L_F2" sourceRef="Sub_1" targetRef="L_End"/>
  </bpmn:process>
</bpmn:definitions>`, deptID, validCandidateUser)
}

func TestValidateModule_ALaneWhoseDeptIDIsNotAUUIDIsRefusedEvenWhenNoTaskSitsDirectlyInIt(t *testing.T) {
	for _, bad := range []string{"   ", "not-a-uuid", "f14abc9f-befa-54ff-b57f-c15ab3090062x"} {
		t.Run(fmt.Sprintf("dept_id %q", bad), func(t *testing.T) {
			errs, err := bpmn_compiler.New().ValidateModule(context.Background(), moduleWithALaneThatOnlyHostsASubProcess(bad), "Process_L", false)
			require.NoError(t, err)

			var onLane []domain.BPMNValidationError
			for _, e := range errs {
				if e.Code == domain.BPMNErrInvalidDeptID {
					onLane = append(onLane, e)
				}
			}
			require.Len(t, onLane, 1, "errors: %+v", errs)
			assert.Equal(t, "Lane_x", onLane[0].NodeID)
		})
	}
}

func TestValidateModule_ALaneWithAUUIDDeptIDHostingOnlyASubProcessIsAccepted(t *testing.T) {
	errs, err := bpmn_compiler.New().ValidateModule(context.Background(),
		moduleWithALaneThatOnlyHostsASubProcess("f14abc9f-befa-54ff-b57f-c15ab3090062"), "Process_L", false)
	require.NoError(t, err)

	for _, e := range errs {
		assert.NotEqual(t, domain.BPMNErrInvalidDeptID, e.Code, "%+v", e)
	}
}
