package bpmn_compiler_test

import (
	"context"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestValidate_NodeWithTwoOutgoingFlows_NeedsAGateway(t *testing.T) {
	lane := []flowLane{{"Engineering", engDeptID, []string{"S", "A", "B", "C", "E"}}}
	cases := []struct {
		name, body, node string
	}{
		{
			name: "user task",
			body: "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + userTaskXML("B") + userTaskXML("C") + "    <bpmn:endEvent id=\"E\"/>\n" +
				flowXML("F1", "S", "A") + flowXML("F2", "A", "B") + flowXML("F3", "A", "C") + flowXML("F4", "B", "E") + flowXML("F5", "C", "E"),
			node: "A",
		},
		{
			name: "start event",
			body: "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + userTaskXML("B") + userTaskXML("C") + "    <bpmn:endEvent id=\"E\"/>\n" +
				flowXML("F1", "S", "A") + flowXML("F2", "S", "B") + flowXML("F3", "A", "C") + flowXML("F4", "B", "C") + flowXML("F5", "C", "E"),
			node: "S",
		},
		{
			name: "sub-process",
			body: "    <bpmn:startEvent id=\"S\"/>\n" +
				"    <bpmn:subProcess id=\"A\"><bpmn:startEvent id=\"A_S\"/>\n" + userTaskXML("A_T") + "    <bpmn:endEvent id=\"A_E\"/>\n" +
				flowXML("A_F1", "A_S", "A_T") + flowXML("A_F2", "A_T", "A_E") + "    </bpmn:subProcess>\n" +
				userTaskXML("B") + userTaskXML("C") + "    <bpmn:endEvent id=\"E\"/>\n" +
				flowXML("F1", "S", "A") + flowXML("F2", "A", "B") + flowXML("F3", "A", "C") + flowXML("F4", "B", "E") + flowXML("F5", "C", "E"),
			node: "A",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs, err := bpmn_compiler.New().Validate(context.Background(), flowDocument(lane, tc.body))
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			var found bool
			for _, e := range errs {
				if e.Code == domain.BPMNErrSplitWithoutGateway && e.NodeID == tc.node {
					found = true
				}
			}
			if !found {
				t.Errorf("errors = %v, want SPLIT_WITHOUT_GATEWAY on %s", errCodes(errs), tc.node)
			}
		})
	}
}

func TestValidate_TaskWithTwoOutgoingFlows_IsNotCompiled(t *testing.T) {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + userTaskXML("A") + userTaskXML("B") + userTaskXML("C") + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "B") + flowXML("F3", "A", "C") + flowXML("F4", "B", "E") + flowXML("F5", "C", "E")
	bpmnXML := flowDocument([]flowLane{{"Engineering", engDeptID, []string{"S", "A", "B", "C", "E"}}}, body)

	assertCompileRefusedWith(t, bpmnXML, domain.BPMNErrSplitWithoutGateway)
}
