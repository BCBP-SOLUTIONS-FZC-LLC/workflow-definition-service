package bpmn_compiler_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func fork(kind, split, join string, extraFlows ...string) string {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") +
		"    <bpmn:" + kind + " id=\"" + split + "\"/>\n" + userTaskXML("B") + userTaskXML("C") +
		"    <bpmn:" + kind + " id=\"" + join + "\"/>\n" + userTaskXML("D") + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", split)
	if len(extraFlows) == 0 {
		body += flowXML("F3", split, "B") + flowXML("F4", split, "C")
	} else {
		body += strings.Join(extraFlows, "")
	}
	body += flowXML("F5", "B", join) + flowXML("F6", "C", join) + flowXML("F7", join, "D") + flowXML("F8", "D", "E")
	return flowDocument([]flowLane{
		{"Engineering", engDeptID, []string{"S", "A", split, join, "D", "E"}},
		{"Ops", opsDeptID, []string{"B", "C"}},
	}, body)
}

func TestCompileForPublish_InclusiveGatewayWithoutConditions_CompilesLikeAParallelGateway(t *testing.T) {
	inclusive := compileForPublish(t, fork("inclusiveGateway", "G1", "J1"))
	parallel := compileForPublish(t, fork("parallelGateway", "G1", "J1"))

	got := runOrder(t, inclusive)
	sorted := slices.Sorted(slices.Values(got))
	if !slices.Equal(sorted, []string{"A", "B", "C", "D"}) || got[0] != "A" || got[len(got)-1] != "D" {
		t.Errorf("tasks run = %v, want A, then B and C, then D", got)
	}
	if !reflect.DeepEqual(expandedMain(t, inclusive).Execution, expandedMain(t, parallel).Execution) {
		t.Errorf("an inclusive fork without conditions compiled differently from the parallel fork:\n%+v\n%+v",
			expandedMain(t, inclusive).Execution, expandedMain(t, parallel).Execution)
	}
}

func TestCompileForPublish_InclusiveGatewayWithAConditionedBranch_IsRefused(t *testing.T) {
	doc := fork("inclusiveGateway", "G1", "J1",
		conditionalFlowXML("F3", "G1", "B", `x == "big"`), flowXML("F4", "G1", "C"))

	_, err := bpmn_compiler.New().CompileForPublish(context.Background(), doc)

	var vf *domain.ValidationFailedError
	if !errors.As(err, &vf) {
		t.Fatalf("CompileForPublish error = %v, want a validation failure", err)
	}
	var found bool
	for _, e := range vf.Errors {
		if e.Code == domain.BPMNErrUnsupportedElement && e.NodeID == "G1" && strings.Contains(e.Message, "condition") {
			found = true
		}
	}
	if !found {
		t.Errorf("errors = %+v, want UNSUPPORTED_ELEMENT on G1 naming conditions", vf.Errors)
	}
}

func TestCompileForPublish_InclusiveGatewayWithoutAJoin_IsUnmatched(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + "    <bpmn:inclusiveGateway id=\"G1\"/>\n" +
		userTaskXML("B") + userTaskXML("C") + "    <bpmn:endEvent id=\"E1\"/>\n    <bpmn:endEvent id=\"E2\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "G1") + flowXML("F3", "G1", "B") + flowXML("F4", "G1", "C") +
		flowXML("F5", "B", "E1") + flowXML("F6", "C", "E2")
	doc := flowDocument([]flowLane{
		{"Engineering", engDeptID, []string{"S", "A", "G1", "E1", "E2"}},
		{"Ops", opsDeptID, []string{"B", "C"}},
	}, body)

	_, err := bpmn_compiler.New().CompileForPublish(context.Background(), doc)

	var vf *domain.ValidationFailedError
	if !errors.As(err, &vf) {
		t.Fatalf("CompileForPublish error = %v, want a validation failure", err)
	}
	if !slices.ContainsFunc(vf.Errors, func(e domain.BPMNValidationError) bool {
		return e.Code == domain.BPMNErrUnmatchedGateway && e.NodeID == "G1"
	}) {
		t.Errorf("errors = %+v, want UNMATCHED_GATEWAY on G1", vf.Errors)
	}
}
