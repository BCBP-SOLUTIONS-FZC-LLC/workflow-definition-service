package bpmn_compiler_test

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const (
	gatewaysOnly  = "    <bpmn:exclusiveGateway id=\"T\"/>\n    <bpmn:exclusiveGateway id=\"X\"/>\n"
	revertCondA   = `x == "a"`
	revertCondB   = `x == "b"`
	revertCondEnd = `x == "done"`
)

func TestValidate_RevertThatReachesNoTask_IsRefused(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + gatewaysOnly + "    <bpmn:endEvent id=\"E\"/>\n    <bpmn:endEvent id=\"E2\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "T") +
		conditionalFlowXML("F3", "T", "X", revertCondA) + conditionalFlowXML("F4", "T", "E2", revertCondB) +
		conditionalFlowXML("F5", "X", "E", revertCondEnd) + conditionalFlowXML("F6", "X", "T", revertCondA)
	doc := flowDocument(oneLane("S", "A", "T", "X", "E", "E2"), body)

	assertErrorOn(t, validateDocument(t, doc), domain.BPMNErrUnsupportedRevertTarget, "X")
	assertErrorOn(t, validateForPublishDocument(t, doc), domain.BPMNErrUnsupportedRevertTarget, "X")
}

func TestValidate_RevertWhoseOtherBranchLeadsToACall_IsRefused(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + gatewaysOnly + callXML("C", "Mod") + "    <bpmn:endEvent id=\"E\"/>\n    <bpmn:endEvent id=\"E2\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "T") +
		conditionalFlowXML("F3", "T", "E2", revertCondB) + conditionalFlowXML("F4", "T", "C", revertCondA) + flowXML("F5", "C", "X") +
		conditionalFlowXML("F6", "X", "E", revertCondEnd) + conditionalFlowXML("F7", "X", "T", revertCondA)
	doc := flowDocument(oneLane("S", "A", "T", "X", "C", "E", "E2"), body, lanedModule("Mod", moduleLane{"Rev", "", "M_T"}))

	assertErrorOn(t, validateDocument(t, doc), domain.BPMNErrUnsupportedRevertTarget, "X")
	assertErrorOn(t, validateForPublishDocument(t, doc), domain.BPMNErrUnsupportedRevertTarget, "X")
}

func TestValidate_RevertWhoseOtherBranchLeadsToASubProcess_IsRefused(t *testing.T) {
	sub := "    <bpmn:subProcess id=\"SP\"><bpmn:startEvent id=\"SP_S\"/>\n" + userTaskXML("SP_T") + "    <bpmn:endEvent id=\"SP_E\"/>\n" +
		flowXML("SP_F1", "SP_S", "SP_T") + flowXML("SP_F2", "SP_T", "SP_E") + "    </bpmn:subProcess>\n"
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + gatewaysOnly + sub + "    <bpmn:endEvent id=\"E\"/>\n    <bpmn:endEvent id=\"E2\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "T") +
		conditionalFlowXML("F3", "T", "E2", revertCondB) + conditionalFlowXML("F4", "T", "SP", revertCondA) + flowXML("F5", "SP", "X") +
		conditionalFlowXML("F6", "X", "E", revertCondEnd) + conditionalFlowXML("F7", "X", "T", revertCondA)
	doc := flowDocument(oneLane("S", "A", "T", "X", "SP", "E", "E2"), body)

	assertErrorOn(t, validateDocument(t, doc), domain.BPMNErrUnsupportedRevertTarget, "X")
}

func TestValidate_RevertIntoAGatewayWhoseFirstBranchReachesNoTask_IsRefused(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + userTaskXML("B") + gatewaysOnly + "    <bpmn:endEvent id=\"E\"/>\n    <bpmn:endEvent id=\"E2\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "T") +
		conditionalFlowXML("F3", "T", "E2", revertCondB) + conditionalFlowXML("F4", "T", "B", revertCondA) + flowXML("F5", "B", "X") +
		conditionalFlowXML("F6", "X", "E", revertCondEnd) + conditionalFlowXML("F7", "X", "T", revertCondA)
	doc := flowDocument(oneLane("S", "A", "B", "T", "X", "E", "E2"), body)

	assertErrorOn(t, validateDocument(t, doc), domain.BPMNErrUnsupportedRevertTarget, "X")
	assertErrorOn(t, validateForPublishDocument(t, doc), domain.BPMNErrUnsupportedRevertTarget, "X")
}

func TestValidate_RevertThroughGatewaysToAUserTask_IsAccepted(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + userTaskXML("B") + userTaskXML("C") + gatewaysOnly + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "T") +
		conditionalFlowXML("F3", "T", "B", revertCondA) + conditionalFlowXML("F4", "T", "C", revertCondB) + flowXML("F5", "B", "X") + flowXML("F6", "C", "X") +
		conditionalFlowXML("F7", "X", "E", revertCondEnd) + conditionalFlowXML("F8", "X", "T", revertCondA)
	doc := flowDocument(oneLane("S", "A", "B", "C", "T", "X", "E"), body)

	assertNoError(t, validateDocument(t, doc), domain.BPMNErrUnsupportedRevertTarget)
}
