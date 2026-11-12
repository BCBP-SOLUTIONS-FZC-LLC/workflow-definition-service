package bpmn_compiler_test

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestValidate_GatewayWithTwoIdenticalFlows_IsRefused(t *testing.T) {
	for _, kind := range []string{"parallelGateway", "exclusiveGateway", "inclusiveGateway"} {
		t.Run(kind, func(t *testing.T) {
			doc := fork(kind, "G1", "J1",
				flowXML("F3", "G1", "B"), flowXML("F3_again", "G1", "B"), flowXML("F4", "G1", "C"))

			assertErrorOn(t, validateDocument(t, doc), domain.BPMNErrDuplicateSequenceFlow, "G1")
			assertErrorOn(t, validateForPublishDocument(t, doc), domain.BPMNErrDuplicateSequenceFlow, "G1")
			assertNoError(t, validateDocument(t, doc), domain.BPMNErrSplitWithoutGateway)
		})
	}
}

func TestValidate_ExclusiveGatewayWithTwoIdenticalConditionedFlows_IsRefused(t *testing.T) {
	doc := fork("exclusiveGateway", "G1", "J1",
		conditionalFlowXML("F3", "G1", "B", `x == "a"`), conditionalFlowXML("F3_again", "G1", "B", `x == "b"`), flowXML("F4", "G1", "C"))

	assertErrorOn(t, validateDocument(t, doc), domain.BPMNErrDuplicateSequenceFlow, "G1")
}

func TestValidate_GatewayInsideASubProcessWithTwoIdenticalFlows_IsRefused(t *testing.T) {
	sub := "    <bpmn:subProcess id=\"SP\"><bpmn:startEvent id=\"SP_S\"/>\n" + userTaskXML("SP_A") +
		"    <bpmn:parallelGateway id=\"SP_G\"/>\n    <bpmn:parallelGateway id=\"SP_J\"/>\n" + userTaskXML("SP_B") + userTaskXML("SP_C") +
		"    <bpmn:endEvent id=\"SP_E\"/>\n" +
		flowXML("SP_F1", "SP_S", "SP_A") + flowXML("SP_F2", "SP_A", "SP_G") +
		flowXML("SP_F3", "SP_G", "SP_B") + flowXML("SP_F3_again", "SP_G", "SP_B") + flowXML("SP_F4", "SP_G", "SP_C") +
		flowXML("SP_F5", "SP_B", "SP_J") + flowXML("SP_F6", "SP_C", "SP_J") + flowXML("SP_F7", "SP_J", "SP_E") +
		"    </bpmn:subProcess>\n"
	body := "    <bpmn:startEvent id=\"S\"/>\n" + sub + "    <bpmn:endEvent id=\"E\"/>\n" + flowXML("F1", "S", "SP") + flowXML("F2", "SP", "E")
	errs := validateDocument(t, flowDocument(oneLane("S", "SP", "E"), body))

	assertErrorOn(t, errs, domain.BPMNErrDuplicateSequenceFlow, "SP_G")
}

func TestValidate_GatewayWithDistinctTargets_HasNoSplitError(t *testing.T) {
	for _, kind := range []string{"parallelGateway", "exclusiveGateway", "inclusiveGateway"} {
		t.Run(kind, func(t *testing.T) {
			errs := validateDocument(t, fork(kind, "G1", "J1"))
			assertNoError(t, errs, domain.BPMNErrSplitWithoutGateway)
			assertNoError(t, errs, domain.BPMNErrDuplicateSequenceFlow)
		})
	}
}
