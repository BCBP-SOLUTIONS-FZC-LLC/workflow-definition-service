package bpmn_compiler_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const messageBoundaryDef = `<bpmn:messageEventDefinition messageRef="M1"/>`

const messageDefinition = `  <bpmn:message id="M1" name="msg1"/>`

func boundaryXML(id, host, definition string) string {
	return fmt.Sprintf("    <bpmn:boundaryEvent id=%q attachedToRef=%q>%s</bpmn:boundaryEvent>\n", id, host, definition)
}

func receiveTaskXML(id string) string {
	return fmt.Sprintf("    <bpmn:receiveTask id=%q messageRef=\"M1\"/>\n", id)
}

func validateDocument(t *testing.T, bpmnXML string) []domain.BPMNValidationError {
	t.Helper()
	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return errs
}

func assertErrorOn(t *testing.T, errs []domain.BPMNValidationError, code domain.BPMNErrorCode, node string) {
	t.Helper()
	for _, e := range errs {
		if e.Code == code && e.NodeID == node {
			return
		}
	}
	t.Errorf("errors = %+v, want %s on %s", errs, code, node)
}

func assertNoError(t *testing.T, errs []domain.BPMNValidationError, code domain.BPMNErrorCode) {
	t.Helper()
	for _, e := range errs {
		if e.Code == code {
			t.Errorf("unexpected %s on %s: %s", code, e.NodeID, e.Message)
		}
	}
}

func oneLane(nodes ...string) []flowLane {
	return []flowLane{{"Engineering", engDeptID, nodes}}
}

func TestValidate_TimerOrMessageBoundaryOnAReceiveTask_IsRefused(t *testing.T) {
	for name, definition := range map[string]string{"timer": timerP1D, "message": messageBoundaryDef} {
		t.Run(name, func(t *testing.T) {
			body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + receiveTaskXML("R") + userTaskXML("B") + "    <bpmn:endEvent id=\"E\"/>\n" +
				boundaryXML("BE", "R", definition) +
				flowXML("F1", "S", "A") + flowXML("F2", "A", "R") + flowXML("F3", "R", "E") + flowXML("F4", "BE", "B") + flowXML("F5", "B", "E")
			errs := validateDocument(t, flowDocument(oneLane("S", "A", "R", "B", "E"), body, messageDefinition))

			assertErrorOn(t, errs, domain.BPMNErrInvalidBoundaryAttachment, "BE")
		})
	}
}

func TestValidate_TwoMessageBoundariesOnOneTask_IsRefused(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + userTaskXML("B") + userTaskXML("C") + "    <bpmn:endEvent id=\"E\"/>\n" +
		boundaryXML("B1", "A", messageBoundaryDef) + boundaryXML("B2", "A", messageBoundaryDef) +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "E") + flowXML("F3", "B1", "B") + flowXML("F4", "B", "E") + flowXML("F5", "B2", "C") + flowXML("F6", "C", "E")
	errs := validateDocument(t, flowDocument(oneLane("S", "A", "B", "C", "E"), body, messageDefinition))

	assertErrorOn(t, errs, domain.BPMNErrInvalidBoundaryAttachment, "B2")
}

func TestValidate_BoundaryWithTwoOutgoingFlows_NeedsAGateway(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + userTaskXML("B") + userTaskXML("C") + "    <bpmn:endEvent id=\"E\"/>\n" +
		boundaryXML("BE", "A", timerP1D) +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "E") + flowXML("F3", "BE", "B") + flowXML("F4", "BE", "C") + flowXML("F5", "B", "E") + flowXML("F6", "C", "E")
	errs := validateDocument(t, flowDocument(oneLane("S", "A", "B", "C", "E"), body))

	assertErrorOn(t, errs, domain.BPMNErrSplitWithoutGateway, "BE")
}

func TestValidate_ReceiveTaskCannotBeTheSourceOfARevertFlow(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + receiveTaskXML("R") + userTaskXML("B") + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "R") + flowXML("F3", "R", "B") + flowXML("F4", "B", "E") + flowXML("F5", "R", "A")
	errs := validateDocument(t, flowDocument(oneLane("S", "A", "R", "B", "E"), body, messageDefinition))

	assertErrorOn(t, errs, domain.BPMNErrUnguardedLoop, "R")
}

func TestValidate_EndEventWithAnOutgoingFlow_IsRefused(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + userTaskXML("B") + "    <bpmn:endEvent id=\"E\"/>\n    <bpmn:endEvent id=\"E2\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "E") + flowXML("F3", "E", "B") + flowXML("F4", "B", "E2")
	errs := validateDocument(t, flowDocument(oneLane("S", "A", "B", "E", "E2"), body))

	assertErrorOn(t, errs, domain.BPMNErrEndEventWithOutgoingFlow, "E")
}

func exclusiveSplit(firstCondition, secondCondition string) string {
	flow := func(id, to, condition string) string {
		if condition == "" {
			return flowXML(id, "X", to)
		}
		return conditionalFlowXML(id, "X", to, condition)
	}
	return "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + "    <bpmn:exclusiveGateway id=\"X\"/>\n    <bpmn:exclusiveGateway id=\"J\"/>\n" +
		userTaskXML("B") + userTaskXML("C") + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "X") + flow("F3", "B", firstCondition) + flow("F4", "C", secondCondition) +
		flowXML("F5", "B", "J") + flowXML("F6", "C", "J") + flowXML("F7", "J", "E")
}

func TestValidate_ExclusiveSplitWithTwoBranchesAndNoCondition_Warns(t *testing.T) {
	errs := validateDocument(t, flowDocument(oneLane("S", "A", "X", "J", "B", "C", "E"), exclusiveSplit("", "")))

	assertErrorOn(t, errs, domain.BPMNWarnGatewayBranchWithoutCondition, "X")
	if blocking := blockingErrors(errs); len(blocking) > 0 {
		t.Errorf("a warning must not block publishing: %+v", blocking)
	}
}

func TestValidate_ExclusiveSplitWithOneDefaultBranch_IsValid(t *testing.T) {
	errs := validateDocument(t, flowDocument(oneLane("S", "A", "X", "J", "B", "C", "E"), exclusiveSplit("x == \"y\"", "")))

	assertNoError(t, errs, domain.BPMNWarnGatewayBranchWithoutCondition)
}

func TestValidate_TwoElementsWithOneID_IsRefused(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + userTaskXML("A") + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "E")
	errs := validateDocument(t, flowDocument(oneLane("S", "A", "E"), body))

	assertErrorOn(t, errs, domain.BPMNErrDuplicateElementID, "A")
}

func TestValidate_ErrorBoundaryOnACallWithAnUnknownErrorRef_IsRefused(t *testing.T) {
	call := callXML("CA", "Mod")
	errorBoundary := `<bpmn:errorEventDefinition errorRef="Nope"/>`
	body := "    <bpmn:startEvent id=\"S\"/>\n" + call + userTaskXML("B") + "    <bpmn:endEvent id=\"E\"/>\n" +
		boundaryXML("BE", "CA", errorBoundary) +
		flowXML("F1", "S", "CA") + flowXML("F2", "CA", "E") + flowXML("F3", "BE", "B") + flowXML("F4", "B", "E")
	errs := validateDocument(t, flowDocument(oneLane("S", "CA", "B", "E"), body, lanedModule("Mod", moduleLane{"Rev", "", "M_T"})))

	assertErrorOn(t, errs, domain.BPMNErrInvalidBoundaryAttachment, "BE")
}

func TestValidate_SubProcessMissingFromEveryLane_IsRefused(t *testing.T) {
	sub := "    <bpmn:subProcess id=\"SP\"><bpmn:startEvent id=\"SP_S\"/>\n" + userTaskXML("SP_T") + "    <bpmn:endEvent id=\"SP_E\"/>\n" +
		flowXML("SP_F1", "SP_S", "SP_T") + flowXML("SP_F2", "SP_T", "SP_E") + "    </bpmn:subProcess>\n"
	body := "    <bpmn:startEvent id=\"S\"/>\n" + sub + "    <bpmn:endEvent id=\"E\"/>\n" + flowXML("F1", "S", "SP") + flowXML("F2", "SP", "E")
	errs := validateDocument(t, flowDocument(oneLane("S", "E"), body))

	assertErrorOn(t, errs, domain.BPMNErrTaskNotInLane, "SP")
}

func TestValidate_CallActivityInsideASubProcess_IsNamedAsUnsupported(t *testing.T) {
	sub := "    <bpmn:subProcess id=\"SP\"><bpmn:startEvent id=\"SP_S\"/>\n" + userTaskXML("SP_T") + callXML("SP_C", "Mod") + "    <bpmn:endEvent id=\"SP_E\"/>\n" +
		flowXML("SP_F1", "SP_S", "SP_T") + flowXML("SP_F2", "SP_T", "SP_C") + flowXML("SP_F3", "SP_C", "SP_E") + "    </bpmn:subProcess>\n"
	body := "    <bpmn:startEvent id=\"S\"/>\n" + sub + "    <bpmn:endEvent id=\"E\"/>\n" + flowXML("F1", "S", "SP") + flowXML("F2", "SP", "E")
	errs := validateDocument(t, flowDocument(oneLane("S", "SP", "E"), body))

	assertErrorOn(t, errs, domain.BPMNErrUnsupportedElement, "SP_C")
	assertNoError(t, errs, domain.BPMNErrInvalidSequenceFlowRef)
}
