package bpmn_compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
)

func copySlice[T any](s []T) []T {
	c := make([]T, len(s))
	copy(c, s)
	return c
}

func canonicalHash(proc *bpmncore.BPMNProcess) (string, error) {
	clone := *proc
	clone.UserTasks = copySlice(proc.UserTasks)
	clone.GenericTasks = copySlice(proc.GenericTasks)
	clone.SendTasks = copySlice(proc.SendTasks)
	clone.ReceiveTasks = copySlice(proc.ReceiveTasks)
	clone.StartEvents = copySlice(proc.StartEvents)
	clone.EndEvents = copySlice(proc.EndEvents)
	clone.ParallelGateways = copySlice(proc.ParallelGateways)
	clone.ExclusiveGateways = copySlice(proc.ExclusiveGateways)
	clone.InclusiveGateways = copySlice(proc.InclusiveGateways)
	clone.SequenceFlows = copySlice(proc.SequenceFlows)
	clone.BoundaryEvents = copySlice(proc.BoundaryEvents)
	clone.CallActivities = copySlice(proc.CallActivities)
	clone.DataStoreRefs = copySlice(proc.DataStoreRefs)
	// Shallow copy is sufficient: canonicalHash never mutates sub-process internals.
	clone.SubProcesses = copySlice(proc.SubProcesses)

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
