package validator

import (
	"maps"
	"slices"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func ValidateReachability(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, implicitStart string) []domain.BPMNValidationError {
	startID := ""
	if len(proc.StartEvents) > 0 {
		startID = proc.StartEvents[0].ID
	} else if implicitStart != "" {
		startID = implicitStart
	}
	if startID == "" {
		return nil
	}
	visited := make(map[string]bool)
	bpmncore.MarkReachable(startID, g, visited)

	for _, be := range proc.BoundaryEvents {
		bpmncore.MarkReachable(be.ID, g, visited)
	}

	var errs []domain.BPMNValidationError
	for _, id := range slices.Sorted(maps.Keys(g.NodeIDs)) {
		if bpmncore.IsBoundaryEventType(g.NodeType[id]) {
			continue
		}
		if !visited[id] {
			errs = AppendErr(errs, domain.BPMNErrUnreachableNode, id,
				"node is not reachable from the start event")
		}
	}
	return errs
}
