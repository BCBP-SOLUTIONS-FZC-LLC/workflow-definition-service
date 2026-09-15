package element

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type UserTaskHandler struct{}

func (UserTaskHandler) NodeType() bpmncore.FlowNodeType { return bpmncore.NodeTypeUserTask }

func (UserTaskHandler) Validate(nodeID string, proc *bpmncore.BPMNProcess, _ *bpmncore.Graph, _ *bpmncore.BPMNDefinitions, stageTypes map[string]bpmncore.StageTypeHandler, _ map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	task := bpmncore.FindTask(proc, nodeID)
	if task == nil {
		return nil
	}

	var errs []domain.BPMNValidationError
	if connectorType, ok := bpmncore.ConnectorType(task.ExtensionElements); ok {
		// Connector tasks are fully automation-only — no assignmentDefinition
		// required or expected (design/LLD/workflow_connectors.md §5.3).
		errs = append(errs, validator.ValidateConnectorTaskDef(task.ID, connectorType)...)
		errs = append(errs, validator.ValidateConnectorTaskNoAssignment(task.ID, task.ExtensionElements)...)
	} else {
		errs = append(errs, validator.ValidateTaskDef(task.ID, task.ExtensionElements, stageTypes)...)
		errs = append(errs, validator.ValidateAssignmentDef(task.ID, task.ExtensionElements)...)
	}

	laneRefs := bpmncore.BuildLaneRefSet(proc)
	errs = append(errs, validator.ValidateLaneMembership(task.ID, laneRefs)...)
	errs = append(errs, validator.ValidateDeptID(task.ID, proc)...)
	return errs
}

func (UserTaskHandler) Compile(nodeID string, cs *bpmncore.CompileState) error {
	task := bpmncore.FindTask(cs.Proc, nodeID)
	if task == nil {
		return fmt.Errorf("task %q not found in process", nodeID)
	}
	deptID, label, iamDeptID := cs.DeptOf(nodeID)

	stage, err := bpmncore.BuildStageDef(task, cs.StageTypes, cs.Proc, cs.Defs)
	if err != nil {
		return err
	}
	cs.FillBoundaryTimerTarget(task.ID, &stage)
	cs.FillBoundaryMessageTarget(task.ID, &stage)
	cs.EnsureDept(deptID, label, iamDeptID)
	cs.AppendStage(deptID, stage)
	cs.AddToSeqBuf(deptID)

	if nexts := cs.ForwardNexts(nodeID); len(nexts) > 0 {
		return cs.TraverseNode(nexts[0])
	}
	return nil
}
