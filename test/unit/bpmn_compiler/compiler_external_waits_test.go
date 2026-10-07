package bpmn_compiler_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"
)

func externalWaits(t *testing.T, collab *dsl.CompiledCollaboration) []dsl.ExternalWait {
	t.Helper()
	expanded, err := dsl.ExpandCalls(collab, 0)
	if err != nil {
		t.Fatalf("ExpandCalls: %v", err)
	}
	return dsl.ExternalWaits(expanded)
}

// TestCompileForPublish_ReceiveFedByAnOutsidePool_IsAnExternalWait pins the
// read Tender relies on: the receive task the vendor's bid completes is an
// outside wait, addressed by its node key.
func TestCompileForPublish_ReceiveFedByAnOutsidePool_IsAnExternalWait(t *testing.T) {
	collab := compileForPublish(t, tenderDocument(userTaskXML("Award")))

	got := externalWaits(t, collab)
	want := []dsl.ExternalWait{{Key: dsl.NodeKey("Ops", &dsl.StageDef{NodeID: "Receive_Bid"}), Message: "bid-received"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("external waits = %#v, want %#v", got, want)
	}
}

// TestCompileForPublish_MessageBoundaryFedByAnOutsidePool_IsAnExternalWait
// pins that a message boundary keeps its BPMN element ID, so an outside
// party can name it: Prepare's withdraw boundary is addressed as
// "Ops/Prepare#Boundary_Withdraw".
func TestCompileForPublish_MessageBoundaryFedByAnOutsidePool_IsAnExternalWait(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("Prepare") + userTaskXML("Award") +
		"    <bpmn:boundaryEvent id=\"Boundary_Withdraw\" attachedToRef=\"Prepare\"><bpmn:messageEventDefinition messageRef=\"Msg_withdraw\"/></bpmn:boundaryEvent>\n" +
		"    <bpmn:endEvent id=\"E\"/>\n    <bpmn:endEvent id=\"E2\"/>\n" +
		flowXML("F1", "S", "Prepare") + flowXML("F2", "Prepare", "Award") + flowXML("F3", "Award", "E") + flowXML("F4", "Boundary_Withdraw", "E2")
	doc := poolsDocument([]flowLane{{"Ops", opsDeptID, []string{"S", "Prepare", "Award", "Boundary_Withdraw", "E", "E2"}}}, body,
		"    <bpmn:messageFlow id=\"MF1\" sourceRef=\"V_Send\" targetRef=\"Boundary_Withdraw\"/>\n")
	doc = strings.Replace(doc, `<bpmn:message id="Msg_bid" name="bid-received"/>`,
		`<bpmn:message id="Msg_bid" name="bid-received"/><bpmn:message id="Msg_withdraw" name="withdraw"/>`, 1)

	got := externalWaits(t, compileForPublish(t, doc))
	want := []dsl.ExternalWait{{Key: "Ops/Prepare#Boundary_Withdraw", Message: "withdraw", Boundary: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("external waits = %#v, want %#v", got, want)
	}
}

// TestCompileForPublish_Bnb_ListsTheOutsideWaits pins the real example: the
// receive task inside the module the contractor pool calls, which waits for
// its own id since no message names it, and the rfq-issued boundary on the
// call itself, addressed by the call's id.
func TestCompileForPublish_Bnb_ListsTheOutsideWaits(t *testing.T) {
	got := externalWaits(t, compileForPublish(t, mustReadBPMN(t, "bnb.bpmn")))

	want := []dsl.ExternalWait{
		{Key: "Activity_0tcolgg::Receiver/Activity_0zz5tcp", Message: "Activity_0zz5tcp"},
		{Key: "Activity_0tcolgg#Event_1hh5nsx", Message: "rfq-issued", Boundary: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("external waits = %#v, want %#v", got, want)
	}
}
