package element

import "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"

// compileInnerState builds a CompileState for a nested process — a
// subprocess body or a called process — and runs it from that process's
// start event, ready for the caller to harvest CollectedDepts/CollectedSteps.
func compileInnerState(inner *bpmncore.BPMNProcess, cs *bpmncore.CompileState) (*bpmncore.CompileState, error) {
	innerG := bpmncore.BuildGraph(inner)
	innerLaneLabels := bpmncore.BuildLaneLabels(inner)
	innerCondExprs := bpmncore.BuildCondExprs(inner)
	innerBackEdges := map[[2]string]bool{}
	if len(inner.StartEvents) > 0 {
		innerBackEdges = bpmncore.ClassifyBackEdges(innerG, inner.StartEvents[0].ID)
	}

	innerState := bpmncore.NewCompileState(inner, innerG, innerLaneLabels, innerCondExprs, innerBackEdges, cs.StageTypes, cs.Elements, cs.Defs)
	if len(inner.StartEvents) > 0 {
		if nexts := innerState.ForwardNexts(inner.StartEvents[0].ID); len(nexts) > 0 {
			if err := innerState.TraverseNode(nexts[0]); err != nil {
				return nil, err
			}
		}
	}
	if err := innerState.CompileBoundaryPaths(); err != nil {
		return nil, err
	}
	return innerState, nil
}
