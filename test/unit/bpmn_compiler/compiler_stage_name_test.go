package bpmn_compiler_test

import (
	"context"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
)

// TestCompile_StageCarriesTheTaskName: every kind of stage carries its task's
// name, which the node and module-contract reads show.
func TestCompile_StageCarriesTheTaskName(t *testing.T) {
	for _, tc := range []struct{ name, task string }{
		{"user task", `<bpmn:userTask id="Task_1" name="Prepare the response">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="design" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_1</bpmn:incoming><bpmn:outgoing>Flow_2</bpmn:outgoing>
    </bpmn:userTask>`},
		{"connector", `<bpmn:serviceTask id="Task_1" name="Prepare the response">
      <bpmn:extensionElements><zeebe:taskDefinition type="connector:storage"/></bpmn:extensionElements>
      <bpmn:incoming>Flow_1</bpmn:incoming><bpmn:outgoing>Flow_2</bpmn:outgoing>
    </bpmn:serviceTask>`},
		{"send task", `<bpmn:sendTask id="Task_1" name="Prepare the response">
      <bpmn:extensionElements><zeebe:assignmentDefinition candidateGroups="design" candidateUsers="` + aliceUUID + `"/></bpmn:extensionElements>
      <bpmn:incoming>Flow_1</bpmn:incoming><bpmn:outgoing>Flow_2</bpmn:outgoing>
    </bpmn:sendTask>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bpmn := compilableBPMN(`
    <bpmn:startEvent id="StartEvent_1"><bpmn:outgoing>Flow_1</bpmn:outgoing></bpmn:startEvent>
    `+tc.task+`
    <bpmn:endEvent id="EndEvent_1"><bpmn:incoming>Flow_2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Task_1"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Task_1" targetRef="EndEvent_1"/>`, "")
			plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			if got := plan.Departments[0].Stages[0].Name; got != "Prepare the response" {
				t.Errorf("Name = %q, want the task's name", got)
			}
		})
	}
}
