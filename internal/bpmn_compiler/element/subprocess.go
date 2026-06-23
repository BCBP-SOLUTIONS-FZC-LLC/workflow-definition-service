package element

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type SubProcessHandler struct{}

func (SubProcessHandler) NodeType() bpmncore.FlowNodeType { return bpmncore.NodeTypeSubProcess }
func (SubProcessHandler) ActivityKind() string            { return "subProcess" }

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
	if err := innerState.CompileTimerBoundaryPaths(); err != nil {
		return fmt.Errorf("subprocess %q: %w", nodeID, err)
	}

	for _, dept := range innerState.CollectedDepts() {
		cs.EnsureDept(dept.ID, dept.Label)
		for _, stage := range dept.Stages {
			cs.AppendStage(dept.ID, stage)
		}
	}

	var errorPaths []domain.ErrorPath
	var timerPaths []domain.TimerPath
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
	}

	cs.FlushSeqBuf()
	cs.AppendStep(domain.ExecutionStep{
		SubWorkflow: &domain.SubWorkflowStep{
			Name:       sp.Name,
			Plan:       domain.ExecutionPlan{Steps: innerState.CollectedSteps()},
			ErrorPaths: errorPaths,
			TimerPaths: timerPaths,
		},
	})

	fwd := cs.ForwardNexts(nodeID)
	if len(fwd) > 0 {
		return cs.TraverseNode(fwd[0])
	}
	return nil
}
