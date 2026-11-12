package bpmn_compiler

import (
	"slices"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
)

func TestValidateDanglingNodes_ImplicitStartOfAnyKindIsNotFlaggedForNoIncomingFlow(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		extra bool
		want  []string
	}{
		{name: "the only node without an incoming flow is chosen and exempt", want: nil},
		{name: "two nodes without an incoming flow leave no implicit start, so both are flagged", extra: true, want: []string{"DANGLING_NODE@X:incoming", "DANGLING_NODE@Y:incoming"}},
	}
	for _, kind := range slices.Concat(w4bTaskLikeKinds, w4bGatewayKinds) {
		for _, tc := range cases {
			t.Run(kind.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				proc := &bpmncore.BPMNProcess{EndEvents: w4bEvents("E")}
				kind.add(proc, "X")
				flows := []string{"X>E"}
				if tc.extra {
					kind.add(proc, "Y")
					flows = append(flows, "Y>E")
				}
				proc.SequenceFlows = w4bFlows(flows...)
				g := bpmncore.BuildGraph(proc)
				implicit := bpmncore.FindImplicitStart(proc, g)
				if !tc.extra && implicit != "X" {
					t.Fatalf("FindImplicitStart = %q, want X", implicit)
				}

				errs := validator.ValidateDanglingNodes(proc, g, implicit)

				w4bCompare(t, w4bSigWithDirection(errs), w4bWant(tc.want...), errs)
			})
		}
	}
}
