package element

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type SendTaskHandler struct{}

func (SendTaskHandler) NodeType() bpmncore.FlowNodeType { return bpmncore.NodeTypeSendTask }

func (SendTaskHandler) Validate(_ string, _ *bpmncore.BPMNProcess, _ *bpmncore.Graph, _ *bpmncore.BPMNDefinitions, _ map[string]bpmncore.StageTypeHandler, _ map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	return nil
}

func (SendTaskHandler) Compile(nodeID string, cs *bpmncore.CompileState) error {
	task := bpmncore.FindSendTask(cs.Proc, nodeID)
	if task == nil {
		return fmt.Errorf("sendTask %q not found in process", nodeID)
	}

	msgName := bpmncore.ResolveMessageName(task.MessageRef, cs.Defs)
	stage := bpmncore.BuildMessageStageDef(task.ID, task.Name, "send_task", msgName, task.ExtensionElements)

	deptID, label := cs.DeptOf(nodeID)
	cs.EnsureDept(deptID, label)
	cs.AppendStage(deptID, stage)
	cs.AddToSeqBuf(deptID)

	nexts := cs.ForwardNexts(nodeID)
	if len(nexts) > 0 {
		return cs.TraverseNode(nexts[0])
	}
	return nil
}
