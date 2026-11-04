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

	errs := checkSplitJoin("gw", "exclusive", g, backEdges, bpmncore.NewJoins(g, backEdges))

	if !hasCode(errs, domain.BPMNErrUnmatchedGateway) {
		t.Fatalf("checkSplitJoin() = %v, want UNMATCHED_GATEWAY for the non-reconverging branch", errs)
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
