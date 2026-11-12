package bpmn_compiler_test

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const connectorTaskXML = `<bpmn:serviceTask id="Task_construction_prep" name="Construction Prep">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="connector:http"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>`

var constructionPrepTask = regexp.MustCompile(`(?s)<bpmn:userTask id="Task_construction_prep".*?</bpmn:userTask>`)

func connectorStageIn(t *testing.T, plan *dsl.CompiledPlan, nodeID string) dsl.StageDef {
	t.Helper()
	for _, dept := range plan.Departments {
		for _, stage := range dept.Stages {
			if stage.NodeID == nodeID {
				return stage
			}
		}
	}
	t.Fatalf("no stage with node id %q in the plan", nodeID)
	return dsl.StageDef{}
}

func TestCompile_RevertToAConnectorTask_NamesTheSameStageTypeAsTheStage(t *testing.T) {
	t.Parallel()
	bpmn := constructionPrepTask.ReplaceAllString(mustReadBPMN(t, "loop-diagram.bpmn"), connectorTaskXML)
	require.Contains(t, bpmn, `type="connector:http"`)

	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	require.NoError(t, err)

	stage := connectorStageIn(t, plan, "Task_construction_prep")
	require.Equal(t, "connector", stage.Type)
	var revert *dsl.ExclusiveBranch
	for _, step := range plan.Execution.Steps {
		for i := range step.Exclusive {
			if step.Exclusive[i].RevertToDept != "" {
				revert = &step.Exclusive[i]
			}
		}
	}
	require.NotNil(t, revert, "the plan has a revert branch")
	assert.Equal(t, "Task_construction_prep", revert.RevertToNodeID)
	assert.Equal(t, stage.Type, revert.RevertToStage)
	assert.Equal(t, "construction/Task_construction_prep", dsl.NodeKey(revert.RevertToDept, &stage))
}

func TestCompile_RevertToAnOrdinaryTask_KeepsItsTaskDefinitionType(t *testing.T) {
	t.Parallel()

	plan, err := bpmn_compiler.New().Compile(context.Background(), mustReadBPMN(t, "loop-diagram.bpmn"))
	require.NoError(t, err)

	stage := connectorStageIn(t, plan, "Task_construction_prep")
	for _, step := range plan.Execution.Steps {
		for _, b := range step.Exclusive {
			if b.RevertToDept != "" {
				assert.Equal(t, "prep", b.RevertToStage)
				assert.Equal(t, stage.Type, b.RevertToStage)
			}
		}
	}
}

func TestCompile_ExclusiveBranchToAConnectorTask_NamesTheSameStageTypeAsTheStage(t *testing.T) {
	t.Parallel()
	bpmn := compilableBPMN(`
    <bpmn:startEvent id="StartEvent_1"><bpmn:outgoing>Flow_1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:exclusiveGateway id="XOR_1"/>
    <bpmn:serviceTask id="Task_1" name="Fetch Packet">
      <bpmn:extensionElements><zeebe:taskDefinition type="connector:storage"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:exclusiveGateway id="Join_1"/>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="XOR_1"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="XOR_1" targetRef="Task_1"><bpmn:conditionExpression>= go = true</bpmn:conditionExpression></bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Flow_3" sourceRef="XOR_1" targetRef="Join_1"><bpmn:conditionExpression>= go = false</bpmn:conditionExpression></bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="Flow_4" sourceRef="Task_1" targetRef="Join_1"/>
    <bpmn:sequenceFlow id="Flow_5" sourceRef="Join_1" targetRef="EndEvent_1"/>`, `
      <bpmndi:BPMNShape id="XOR_1_di" bpmnElement="XOR_1"><dc:Bounds x="200" y="0" width="50" height="50"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Join_1_di" bpmnElement="Join_1"><dc:Bounds x="350" y="0" width="50" height="50"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Flow_3_di" bpmnElement="Flow_3"/>
      <bpmndi:BPMNEdge id="Flow_4_di" bpmnElement="Flow_4"/>
      <bpmndi:BPMNEdge id="Flow_5_di" bpmnElement="Flow_5"/>`)

	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	var invalid *domain.ValidationFailedError
	if errors.As(err, &invalid) {
		t.Fatalf("fixture is invalid: %+v", invalid.Errors)
	}
	require.NoError(t, err)

	stage := connectorStageIn(t, plan, "Task_1")
	require.Equal(t, "connector", stage.Type)
	var found bool
	for _, step := range plan.Execution.Steps {
		for _, b := range step.Exclusive {
			if b.TargetNodeID == "Task_1" {
				found = true
				assert.Equal(t, stage.Type, b.TargetStage)
			}
		}
	}
	assert.True(t, found, "an exclusive branch targets the connector task")
}
