package bpmn_compiler_test

import (
	"context"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func validateForPublishDocument(t *testing.T, bpmnXML string) []domain.BPMNValidationError {
	t.Helper()
	errs, err := bpmn_compiler.New().ValidateForPublish(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("ValidateForPublish: %v", err)
	}
	return errs
}

func TestValidate_SequenceFlowWithAnEmptyReference_IsRefusedOnThatFlow(t *testing.T) {
	straight := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "E")
	subProcess := func(extra string) string {
		return "    <bpmn:startEvent id=\"S\"/>\n    <bpmn:subProcess id=\"SP\"><bpmn:startEvent id=\"SP_S\"/>\n" + userTaskXML("SP_T") +
			"    <bpmn:endEvent id=\"SP_E\"/>\n" + flowXML("SP_F1", "SP_S", "SP_T") + flowXML("SP_F2", "SP_T", "SP_E") + extra +
			"    </bpmn:subProcess>\n    <bpmn:endEvent id=\"E\"/>\n" + flowXML("F1", "S", "SP") + flowXML("F2", "SP", "E")
	}
	cases := map[string]struct {
		body string
		lane []flowLane
		node string
	}{
		"empty sourceRef": {
			body: straight + "    <bpmn:sequenceFlow id=\"F_half\" sourceRef=\"\" targetRef=\"A\"/>\n",
			lane: oneLane("S", "A", "E"), node: "F_half",
		},
		"empty targetRef": {
			body: straight + "    <bpmn:sequenceFlow id=\"F_half\" sourceRef=\"A\" targetRef=\"\"/>\n",
			lane: oneLane("S", "A", "E"), node: "F_half",
		},
		"missing targetRef attribute": {
			body: straight + "    <bpmn:sequenceFlow id=\"F_half\" sourceRef=\"A\"/>\n",
			lane: oneLane("S", "A", "E"), node: "F_half",
		},
		"empty sourceRef in a sub-process": {
			body: subProcess("    <bpmn:sequenceFlow id=\"SP_half\" sourceRef=\"\" targetRef=\"SP_T\"/>\n"),
			lane: oneLane("S", "SP", "E"), node: "SP_half",
		},
		"empty targetRef in a sub-process": {
			body: subProcess("    <bpmn:sequenceFlow id=\"SP_half\" sourceRef=\"SP_T\" targetRef=\"\"/>\n"),
			lane: oneLane("S", "SP", "E"), node: "SP_half",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			doc := flowDocument(tc.lane, tc.body)
			assertErrorOn(t, validateDocument(t, doc), domain.BPMNErrInvalidSequenceFlowRef, tc.node)
			assertErrorOn(t, validateForPublishDocument(t, doc), domain.BPMNErrInvalidSequenceFlowRef, tc.node)
		})
	}
}

func TestValidate_DocumentWithCompleteFlows_HasNoFlowRefError(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "E")
	assertNoError(t, validateDocument(t, flowDocument(oneLane("S", "A", "E"), body)), domain.BPMNErrInvalidSequenceFlowRef)
}
