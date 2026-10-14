package validator

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// TestCheckSplitJoin_BranchDoesNotReconverge builds a graph where a join IS
// found for the split (FindJoin needs incoming(join) >= the split's forward
// out-count, satisfied here via the third "f" feeder), but one of the split's
// own branches ("d") neither forward-reaches that join nor terminates at an
// end event — it loops back on itself instead. checkSplitJoin must reject
// this with UNMATCHED_GATEWAY rather than silently accepting the branch.
func TestCheckSplitJoin_BranchDoesNotReconverge(t *testing.T) {
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{
			"gw": {"b", "c", "d"},
			"b":  {"join"},
			"c":  {"join"},
			"d":  {"e"},
			"e":  {"d"},
			"f":  {"join"},
		},
		Incoming: map[string][]string{
			"b":    {"gw"},
			"c":    {"gw"},
			"d":    {"gw"},
			"e":    {"d"},
			"join": {"b", "c", "f"},
		},
		NodeType: map[string]bpmncore.FlowNodeType{},
	}
	backEdges := map[[2]string]bool{{"e", "d"}: true}

	errs := checkSplitJoin("gw", "exclusive", g, backEdges)

	if !hasCode(errs, domain.BPMNErrUnmatchedGateway) {
		t.Fatalf("checkSplitJoin() = %v, want UNMATCHED_GATEWAY for the non-reconverging branch", errs)
	}
}

// TestBranchTerminatesAtEndEvent_CycleProtection feeds branchTerminatesAtEndEvent
// a cycle (x->y->x) with an empty backEdges map — i.e. neither edge is marked as
// a back-edge, so the function must rely on IterativeDFS's own "scheduled" guard
// to avoid revisiting a node forever. It must return false (no end event reached)
// without hanging.
func TestBranchTerminatesAtEndEvent_CycleProtection(t *testing.T) {
	g := &bpmncore.Graph{
		Outgoing: map[string][]string{
			"x": {"y"},
			"y": {"x"},
		},
		NodeType: map[string]bpmncore.FlowNodeType{},
	}

	if branchTerminatesAtEndEvent("x", g, map[[2]string]bool{}) {
		t.Error("branchTerminatesAtEndEvent() = true, want false (no end event in the cycle)")
	}
}

func hasCode(errs []domain.BPMNValidationError, code domain.BPMNErrorCode) bool {
	for _, e := range errs {
		if e.Code == code {
			return true
		}
	}
	return false
}
