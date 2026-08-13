package bpmn_compiler_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestCompile_ConnectorServiceTask_CompilesWithIOMapping(t *testing.T) {
	bpmn := compilableBPMN(`
    <bpmn:startEvent id="StartEvent_1"><bpmn:outgoing>Flow_1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:serviceTask id="Task_1" name="Fetch Packet">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="connector:storage"/>
        <zeebe:ioMapping>
          <zeebe:input source="=tenderId" target="key"/>
          <zeebe:output source="=contentRef" target="packet_ref"/>
        </zeebe:ioMapping>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_1</bpmn:incoming>
      <bpmn:outgoing>Flow_2</bpmn:outgoing>
    </bpmn:serviceTask>
    <bpmn:endEvent id="EndEvent_1"><bpmn:incoming>Flow_2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Task_1"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Task_1" targetRef="EndEvent_1"/>`, "")

	plan, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() unexpected error: %v", err)
	}
	if len(plan.Departments) == 0 || len(plan.Departments[0].Stages) == 0 {
		t.Fatal("expected at least one stage")
	}
	stage := plan.Departments[0].Stages[0]
	if stage.Type != "connector" {
		t.Errorf("Type = %q; want %q", stage.Type, "connector")
	}
	if stage.ConnectorType != "storage" {
		t.Errorf("ConnectorType = %q; want %q", stage.ConnectorType, "storage")
	}
	if stage.Role != "" || len(stage.DefaultAssignees) != 0 {
		t.Errorf("connector stage must have no assignee fields; got Role=%q DefaultAssignees=%v", stage.Role, stage.DefaultAssignees)
	}
	if stage.EngineNote != "" {
		t.Errorf("connector stage must not carry an unknown-stage-type EngineNote; got %q", stage.EngineNote)
	}
	if stage.IOMapping == nil {
		t.Fatal("expected IOMapping to be populated")
	}
	if len(stage.IOMapping.Inputs) != 1 || stage.IOMapping.Inputs[0].Source != "=tenderId" || stage.IOMapping.Inputs[0].Target != "key" {
		t.Errorf("unexpected Inputs: %+v", stage.IOMapping.Inputs)
	}
	if len(stage.IOMapping.Outputs) != 1 || stage.IOMapping.Outputs[0].Source != "=contentRef" || stage.IOMapping.Outputs[0].Target != "packet_ref" {
		t.Errorf("unexpected Outputs: %+v", stage.IOMapping.Outputs)
	}
}

func TestCompile_NonConnectorServiceTask_StillRejected(t *testing.T) {
	bpmn := compilableBPMN(`
    <bpmn:startEvent id="StartEvent_1"><bpmn:outgoing>Flow_1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:serviceTask id="Task_1" name="Run Script">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="not-a-connector"/>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_1</bpmn:incoming>
      <bpmn:outgoing>Flow_2</bpmn:outgoing>
    </bpmn:serviceTask>
    <bpmn:endEvent id="EndEvent_1"><bpmn:incoming>Flow_2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Task_1"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Task_1" targetRef="EndEvent_1"/>`, "")

	_, err := bpmn_compiler.New().Compile(context.Background(), bpmn)
	var valErr *domain.ValidationFailedError
	if !errors.As(err, &valErr) {
		t.Fatalf("expected *domain.ValidationFailedError; got %T: %v", err, err)
	}
	if !hasCode(valErr.Errors, domain.BPMNErrRejectedElement) {
		t.Errorf("expected REJECTED_ELEMENT; got %v", errCodes(valErr.Errors))
	}
}

func TestValidate_UnknownConnectorType_WarnsWithoutBlocking(t *testing.T) {
	bpmn := compilableBPMN(`
    <bpmn:startEvent id="StartEvent_1"><bpmn:outgoing>Flow_1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:serviceTask id="Task_1" name="Mystery Step">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="connector:not-a-real-connector"/>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_1</bpmn:incoming>
      <bpmn:outgoing>Flow_2</bpmn:outgoing>
    </bpmn:serviceTask>
    <bpmn:endEvent id="EndEvent_1"><bpmn:incoming>Flow_2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Task_1"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Task_1" targetRef="EndEvent_1"/>`, "")

	c := bpmn_compiler.New()
	errs, err := c.Validate(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}
	if !hasCode(errs, domain.BPMNWarnUnknownConnectorType) {
		t.Errorf("expected UNKNOWN_CONNECTOR_TYPE warning; got %v", errCodes(errs))
	}

	plan, err := c.Compile(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("Compile() must still succeed for an unrecognized connector type (warn, not reject): %v", err)
	}
	if len(plan.Departments) == 0 || len(plan.Departments[0].Stages) == 0 {
		t.Fatal("expected at least one stage")
	}
	if got := plan.Departments[0].Stages[0].ConnectorType; got != "not-a-real-connector" {
		t.Errorf("ConnectorType = %q; want %q", got, "not-a-real-connector")
	}
}
