package bpmn_compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
)

func canonicalHash(proc *bpmncore.BPMNProcess) (string, error) {
	clone := *proc
	cloneTasks := make([]bpmncore.BPMNUserTask, len(proc.UserTasks))
	copy(cloneTasks, proc.UserTasks)
	cloneFlows := make([]bpmncore.BPMNSequenceFlow, len(proc.SequenceFlows))
	copy(cloneFlows, proc.SequenceFlows)
	clone.UserTasks = cloneTasks
	clone.SequenceFlows = cloneFlows

	for i := range clone.UserTasks {
		clone.UserTasks[i].ExtensionElements.ZeebeProps.Items = sortedZeebeItems(
			clone.UserTasks[i].ExtensionElements.ZeebeProps.Items,
		)
	}
	for i := range clone.SequenceFlows {
		clone.SequenceFlows[i].ExtensionElements.ZeebeProps.Items = sortedZeebeItems(
			clone.SequenceFlows[i].ExtensionElements.ZeebeProps.Items,
		)
	}

	data, err := json.Marshal(clone)
	if err != nil {
		return "", fmt.Errorf("canonical hash marshal: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func sortedZeebeItems(items []bpmncore.ZeebeProperty) []bpmncore.ZeebeProperty {
	sorted := make([]bpmncore.ZeebeProperty, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].Name < sorted[b].Name })
	return sorted
}
