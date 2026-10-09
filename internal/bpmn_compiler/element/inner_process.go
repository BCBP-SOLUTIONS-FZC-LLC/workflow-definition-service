package element

import "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"

func compileCalledState(called *bpmncore.BPMNProcess, cs *bpmncore.CompileState) (*bpmncore.CompileState, error) {
	state := newInnerState(called, cs)
	state.Called = cs.Called
	if err := runInnerState(called, state); err != nil {
		return nil, err
	}
	state.ResolveTargets()
	return state, nil
}

func compileSubProcessBody(body *bpmncore.BPMNProcess, subProcessID string, cs *bpmncore.CompileState) (*bpmncore.CompileState, error) {
	state := newInnerState(body, cs)
	state.ShareSegmentsWith(cs, subProcessID)
	if err := runInnerState(body, state); err != nil {
		return nil, err
	}
	return state, nil
}

func newInnerState(inner *bpmncore.BPMNProcess, cs *bpmncore.CompileState) *bpmncore.CompileState {
	g := bpmncore.BuildGraph(inner)
	backEdges := map[[2]string]bool{}
	if len(inner.StartEvents) > 0 {
		backEdges = bpmncore.ProcessBackEdges(inner, g, inner.StartEvents[0].ID)
	}
	return bpmncore.NewCompileState(inner, g, bpmncore.BuildLaneLabels(inner), bpmncore.BuildCondExprs(inner), backEdges, cs.StageTypes, cs.Elements, cs.Defs)
}

func runInnerState(inner *bpmncore.BPMNProcess, state *bpmncore.CompileState) error {
	if len(inner.StartEvents) > 0 {
		if nexts := state.ForwardNexts(inner.StartEvents[0].ID); len(nexts) > 0 {
			if err := state.TraverseNode(nexts[0]); err != nil {
				return err
			}
		}
	}
	return state.CompileBoundaryPaths()
}
