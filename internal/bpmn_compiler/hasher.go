package bpmn_compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

func canonicalHash(proc *bpmnProcess) (string, error) {
	clone := *proc
	cloneTasks := make([]bpmnUserTask, len(proc.UserTasks))
	copy(cloneTasks, proc.UserTasks)
	cloneFlows := make([]bpmnSequenceFlow, len(proc.SequenceFlows))
	copy(cloneFlows, proc.SequenceFlows)
	clone.UserTasks = cloneTasks
	clone.SequenceFlows = cloneFlows

	for i := range clone.UserTasks {
		items := clone.UserTasks[i].ExtensionElements.ZeebeProps.Items
		sorted := make([]zeebeProperty, len(items))
		copy(sorted, items)
		sort.Slice(sorted, func(a, b int) bool { return sorted[a].Name < sorted[b].Name })
		clone.UserTasks[i].ExtensionElements.ZeebeProps.Items = sorted
	}
	for i := range clone.SequenceFlows {
		items := clone.SequenceFlows[i].ExtensionElements.ZeebeProps.Items
		sorted := make([]zeebeProperty, len(items))
		copy(sorted, items)
		sort.Slice(sorted, func(a, b int) bool { return sorted[a].Name < sorted[b].Name })
		clone.SequenceFlows[i].ExtensionElements.ZeebeProps.Items = sorted
	}

	data, err := json.Marshal(clone)
	if err != nil {
		return "", fmt.Errorf("canonical hash marshal: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
