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

func canonicalBundleHash(procs []bpmncore.BPMNProcess) (string, error) {
	if len(procs) == 1 {
		return canonicalHash(&procs[0])
	}
	type procHash struct{ id, hash string }
	hashes := make([]procHash, len(procs))
	for i := range procs {
		h, err := canonicalHash(&procs[i])
		if err != nil {
			return "", fmt.Errorf("process %q: %w", procs[i].ID, err)
		}
		hashes[i] = procHash{id: procs[i].ID, hash: h}
	}
	sort.Slice(hashes, func(a, b int) bool {
		if hashes[a].id != hashes[b].id {
			return hashes[a].id < hashes[b].id
		}
		return hashes[a].hash < hashes[b].hash
	})

	bundle := sha256.New()
	for _, ph := range hashes {
		bundle.Write([]byte(ph.hash))
	}
	return hex.EncodeToString(bundle.Sum(nil)), nil
}

func sortedZeebeItems(items []bpmncore.ZeebeProperty) []bpmncore.ZeebeProperty {
	sorted := make([]bpmncore.ZeebeProperty, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].Name < sorted[b].Name })
	return sorted
}
