package element

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"

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
	errs = append(errs, validator.AnchorOn(sp.ID, validator.Validate(inner, innerG, stageTypes, elements, defs))...)

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

	body, err := compileSubProcessBody(bpmncore.SubProcToProcess(sp), nodeID, cs)
	if err != nil {
		return fmt.Errorf("subprocess %q: %w", nodeID, err)
	}
	steps := body.CollectedSteps()
	cs.MarkNodeSegment(nodeID, bpmncore.FirstDepartment(steps))

	errorPaths, timerPaths, messagePaths := boundaryPaths(nodeID, cs)

	cs.FlushSeqBuf()
	cs.AppendStep(dsl.ExecutionStep{
		SubWorkflow: &dsl.SubWorkflowStep{
			NodeID:       sp.ID,
			Name:         sp.Name,
			Plan:         dsl.ExecutionPlan{Steps: steps},
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

// boundaryPaths compiles the boundary events attached to nodeID, a subProcess
// or a callActivity.
func boundaryPaths(nodeID string, cs *bpmncore.CompileState) ([]dsl.ErrorPath, []dsl.TimerPath, []dsl.MessagePath) {
	var errorPaths []dsl.ErrorPath
	var timerPaths []dsl.TimerPath
	var messagePaths []dsl.MessagePath
	for _, be := range cs.Proc.BoundaryEvents {
		if be.AttachedToRef != nodeID {
			continue
		}
		targetDept, terminates := cs.BoundaryTarget(be.ID)
		if be.Error != nil {
			errorPaths = append(errorPaths, dsl.ErrorPath{
				BoundaryID: be.ID,
				ErrorCode:  cs.ResolveErrorCode(be.Error.ErrorRef),
				TargetDept: targetDept,
				Terminates: terminates,
			})
		}
		if be.Timer != nil {
			timerPaths = append(timerPaths, dsl.TimerPath{
				BoundaryID:   be.ID,
				Duration:     be.Timer.Duration,
				Interrupting: be.CancelActivity != "false",
				TargetDept:   targetDept,
				Terminates:   terminates,
			})
		}
		if be.Message != nil {
			msgName := bpmncore.ResolveMessageName(be.Message.MessageRef, cs.Defs)
			if msgName == "" {
				msgName = bpmncore.ResolveMessageFlowTarget(be.ID, cs.Defs)
			}
			messagePaths = append(messagePaths, dsl.MessagePath{
				MessageName:  msgName,
				BoundaryID:   be.ID,
				Interrupting: be.CancelActivity != "false",
				TargetDept:   targetDept,
				Terminates:   terminates,
			})
		}
	}
	return errorPaths, timerPaths, messagePaths
}
