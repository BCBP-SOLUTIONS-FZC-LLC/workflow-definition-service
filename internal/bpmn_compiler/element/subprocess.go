package element

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type SubProcessHandler struct{}

func (SubProcessHandler) NodeType() bpmncore.FlowNodeType { return bpmncore.NodeTypeSubProcess }

func (SubProcessHandler) Validate(nodeID string, proc *bpmncore.BPMNProcess, _ *bpmncore.Graph, defs *bpmncore.BPMNDefinitions, stageTypes map[string]bpmncore.StageTypeHandler, elements map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	sp := bpmncore.FindSubProcess(proc, nodeID)
	if sp == nil {
		return nil
	}

	errorIDs := make(map[string]struct{}, len(defs.Errors))
	for _, e := range defs.Errors {
		errorIDs[e.ID] = struct{}{}
	}

	inner := bpmncore.SubProcToProcess(sp)
	innerG := bpmncore.BuildGraph(inner)
	var errs []domain.BPMNValidationError
	errs = append(errs, validator.Validate(inner, innerG, stageTypes, elements, defs)...)

	for _, be := range proc.BoundaryEvents {
		if be.AttachedToRef != sp.ID || be.Error == nil || be.Error.ErrorRef == "" {
			continue
		}
		if _, ok := errorIDs[be.Error.ErrorRef]; !ok {
			errs = validator.AppendErr(errs, domain.BPMNErrInvalidBoundaryAttachment, be.ID,
				fmt.Sprintf("error boundary event errorRef %q does not reference a known bpmn:error element", be.Error.ErrorRef))
		}
	}
	return errs
}

func (SubProcessHandler) Compile(nodeID string, cs *bpmncore.CompileState) error {
	sp := bpmncore.FindSubProcess(cs.Proc, nodeID)
	if sp == nil {
		return fmt.Errorf("subprocess %q not found in process", nodeID)
	}

	inner := bpmncore.SubProcToProcess(sp)
	innerG := bpmncore.BuildGraph(inner)
	innerLaneLabels := bpmncore.BuildLaneLabels(inner)
	innerCondExprs := bpmncore.BuildCondExprs(inner)
	innerBackEdges := map[[2]string]bool{}
	if len(inner.StartEvents) > 0 {
		innerBackEdges = bpmncore.ClassifyBackEdges(innerG, inner.StartEvents[0].ID)
	}

	innerState := bpmncore.NewCompileState(inner, innerG, innerLaneLabels, innerCondExprs, innerBackEdges, cs.StageTypes, cs.Elements, cs.Defs)
	if len(inner.StartEvents) > 0 {
		nexts := innerState.ForwardNexts(inner.StartEvents[0].ID)
		if len(nexts) > 0 {
			if err := innerState.TraverseNode(nexts[0]); err != nil {
				return fmt.Errorf("subprocess %q: %w", nodeID, err)
			}
		}
	}
	if err := innerState.CompileBoundaryPaths(); err != nil {
		return fmt.Errorf("subprocess %q: %w", nodeID, err)
	}

	depts := innerState.CollectedDepts()
	steps := innerState.CollectedSteps()

	// Inner sub-process tasks have no laneSet of their own; their lane comes
	// from the parent process (the sub-process element itself is in a lane).
	if parentLane := bpmncore.LaneNameFor(nodeID, cs.Proc); parentLane != "" {
		for i := range depts {
			if depts[i].ID == "" {
				depts[i].ID = parentLane
				depts[i].Label = parentLane
			}
		}
		patchEmptyDepts(steps, parentLane)
	}

	for _, dept := range depts {
		cs.EnsureDept(dept.ID, dept.Label)
		for _, stage := range dept.Stages {
			cs.AppendStage(dept.ID, stage)
		}
	}

	var errorPaths []domain.ErrorPath
	var timerPaths []domain.TimerPath
	var messagePaths []domain.MessagePath
	for _, be := range cs.Proc.BoundaryEvents {
		if be.AttachedToRef != nodeID {
			continue
		}
		targetDept := cs.FirstDeptAhead(bpmncore.OutgoingTargetOf(be.ID, cs.Proc))
		if be.Error != nil {
			errorCode := cs.ResolveErrorCode(be.Error.ErrorRef)
			errorPaths = append(errorPaths, domain.ErrorPath{
				ErrorCode:  errorCode,
				TargetDept: targetDept,
			})
		}
		if be.Timer != nil {
			timerPaths = append(timerPaths, domain.TimerPath{
				Duration:     be.Timer.Duration,
				Interrupting: be.CancelActivity != "false",
				TargetDept:   targetDept,
			})
		}
		if be.Message != nil {
			msgName := bpmncore.ResolveMessageName(be.Message.MessageRef, cs.Defs)
			if msgName == "" {
				msgName = bpmncore.ResolveMessageFlowTarget(be.ID, cs.Defs)
			}
			messagePaths = append(messagePaths, domain.MessagePath{
				MessageName:  msgName,
				Interrupting: be.CancelActivity != "false",
				TargetDept:   targetDept,
			})
		}
	}

	cs.FlushSeqBuf()
	cs.AppendStep(domain.ExecutionStep{
		SubWorkflow: &domain.SubWorkflowStep{
			NodeID:       sp.ID,
			Name:         sp.Name,
			Plan:         domain.ExecutionPlan{Steps: steps},
			ErrorPaths:   errorPaths,
			TimerPaths:   timerPaths,
			MessagePaths: messagePaths,
		},
	})

	fwd := cs.ForwardNexts(nodeID)
	if len(fwd) > 0 {
		return cs.TraverseNode(fwd[0])
	}
	return nil
}

func patchEmptyDepts(steps []domain.ExecutionStep, lane string) {
	for i := range steps {
		for j := range steps[i].Sequential {
			if steps[i].Sequential[j] == "" {
				steps[i].Sequential[j] = lane
			}
		}
		for j := range steps[i].Parallel {
			if steps[i].Parallel[j].DeptID == "" {
				steps[i].Parallel[j].DeptID = lane
			}
			patchEmptyDepts(steps[i].Parallel[j].Steps, lane)
		}
		for j := range steps[i].Exclusive {
			b := &steps[i].Exclusive[j]
			if b.RevertToStage != "" || b.RevertToDept != "" {
				if b.RevertToDept == "" {
					b.RevertToDept = lane
				}
			} else if !b.Terminates {
				if b.Target == "" {
					b.Target = lane
				}
			}
		}
		if sw := steps[i].SubWorkflow; sw != nil {
			patchEmptyDepts(sw.Plan.Steps, lane)
			for k := range sw.ErrorPaths {
				if sw.ErrorPaths[k].TargetDept == "" {
					sw.ErrorPaths[k].TargetDept = lane
				}
			}
			for k := range sw.TimerPaths {
				if sw.TimerPaths[k].TargetDept == "" {
					sw.TimerPaths[k].TargetDept = lane
				}
			}
		}
	}
}
