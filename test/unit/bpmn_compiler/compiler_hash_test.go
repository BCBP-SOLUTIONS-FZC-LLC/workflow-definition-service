package bpmn_compiler_test

import (
	"context"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func hashOf(t *testing.T, bpmnXML string) string {
	t.Helper()
	h, err := bpmn_compiler.New().Hash(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	return h
}

func receiveFlow(messageDefinition string) string {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + receiveTaskXML("R") + "    <bpmn:endEvent id=\"E\"/>\n" + flowXML("F1", "S", "R") + flowXML("F2", "R", "E")
	return flowDocument(oneLane("S", "R", "E"), body, messageDefinition)
}

func TestHash_ChangesWhenAMessageNameOrAnErrorCodeChanges(t *testing.T) {
	base := hashOf(t, receiveFlow(`  <bpmn:message id="M1" name="msg1"/>`))

	if renamed := hashOf(t, receiveFlow(`  <bpmn:message id="M1" name="msg2"/>`)); renamed == base {
		t.Error("renaming a message changes the compiled plan, so it must change the artifact hash")
	}
	withError := hashOf(t, receiveFlow(`  <bpmn:message id="M1" name="msg1"/><bpmn:error id="Err1" errorCode="E_ONE"/>`))
	changedCode := hashOf(t, receiveFlow(`  <bpmn:message id="M1" name="msg1"/><bpmn:error id="Err1" errorCode="E_TWO"/>`))
	if withError == changedCode {
		t.Error("changing an error code changes the compiled plan, so it must change the artifact hash")
	}
}

func subProcessWithProperties(first, second string) string {
	task := "    <bpmn:userTask id=\"SP_T\"><bpmn:extensionElements><zeebe:taskDefinition type=\"prep\"/><zeebe:assignmentDefinition candidateGroups=\"preparator\" candidateUsers=\"" + validCandidateUser + "\"/><zeebe:properties>" + first + second + "</zeebe:properties></bpmn:extensionElements></bpmn:userTask>\n"
	sub := "    <bpmn:subProcess id=\"SP\"><bpmn:startEvent id=\"SP_S\"/>\n" + task + "    <bpmn:endEvent id=\"SP_E\"/>\n" +
		flowXML("SP_F1", "SP_S", "SP_T") + flowXML("SP_F2", "SP_T", "SP_E") + "    </bpmn:subProcess>\n"
	body := "    <bpmn:startEvent id=\"S\"/>\n" + sub + "    <bpmn:endEvent id=\"E\"/>\n" + flowXML("F1", "S", "SP") + flowXML("F2", "SP", "E")
	return flowDocument(oneLane("S", "SP", "E"), body)
}

func TestHash_IgnoresThePropertyOrderOfATaskInsideASubProcess(t *testing.T) {
	a := `<zeebe:property name="alpha" value="1"/>`
	b := `<zeebe:property name="beta" value="2"/>`

	if hashOf(t, subProcessWithProperties(a, b)) != hashOf(t, subProcessWithProperties(b, a)) {
		t.Error("reordering two properties of a task changes nothing, so it must not change the artifact hash")
	}
	if strings.Compare(hashOf(t, subProcessWithProperties(a, b)), hashOf(t, subProcessWithProperties(a, `<zeebe:property name="beta" value="3"/>`))) == 0 {
		t.Error("changing a property value must change the artifact hash")
	}
}

func TestPlanStageBudget_IsPositiveAndMatchesTheValidatorLimit(t *testing.T) {
	if domain.MaxPlanStages <= 0 {
		t.Fatalf("MaxPlanStages = %d, want a positive budget", domain.MaxPlanStages)
	}
	if validator.MaxUserTasks != domain.MaxPlanStages {
		t.Fatalf("validator task limit %d differs from the expansion budget %d", validator.MaxUserTasks, domain.MaxPlanStages)
	}
}
