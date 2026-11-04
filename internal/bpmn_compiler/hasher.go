package bpmn_compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
)

func canonicalHash(proc *bpmncore.BPMNProcess) (string, error) {
	sortZeebeProperties(reflect.ValueOf(proc))

	data, err := json.Marshal(proc)
	if err != nil {
		return "", fmt.Errorf("canonical hash marshal: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func sortZeebeProperties(v reflect.Value) {
	if v.Kind() == reflect.Pointer {
		if !v.IsNil() {
			sortZeebeProperties(v.Elem())
		}
		return
	}
	if v.Kind() == reflect.Struct {
		for i := 0; i < v.NumField(); i++ {
			sortZeebeProperties(v.Field(i))
		}
		return
	}
	if v.Kind() != reflect.Slice {
		return
	}
	if items, ok := v.Interface().([]bpmncore.ZeebeProperty); ok {
		sort.SliceStable(items, func(a, b int) bool { return items[a].Name < items[b].Name })
		return
	}
	for i := 0; i < v.Len(); i++ {
		sortZeebeProperties(v.Index(i))
	}
}

type referenceData struct {
	Messages     []bpmncore.BPMNMessage     `json:"messages,omitempty"`
	Errors       []bpmncore.BPMNError       `json:"errors,omitempty"`
	MessageFlows []bpmncore.BPMNMessageFlow `json:"message_flows,omitempty"`
}

func (r referenceData) empty() bool {
	return len(r.Messages) == 0 && len(r.Errors) == 0 && len(r.MessageFlows) == 0
}

func definitionReferences(defs *bpmncore.BPMNDefinitions) referenceData {
	ref := referenceData{
		Messages: append([]bpmncore.BPMNMessage(nil), defs.Messages...),
		Errors:   append([]bpmncore.BPMNError(nil), defs.Errors...),
	}
	if defs.Collaboration != nil {
		ref.MessageFlows = append([]bpmncore.BPMNMessageFlow(nil), defs.Collaboration.MessageFlows...)
	}
	sort.Slice(ref.Messages, func(a, b int) bool { return ref.Messages[a].ID < ref.Messages[b].ID })
	sort.Slice(ref.Errors, func(a, b int) bool { return ref.Errors[a].ID < ref.Errors[b].ID })
	sort.Slice(ref.MessageFlows, func(a, b int) bool { return ref.MessageFlows[a].ID < ref.MessageFlows[b].ID })
	return ref
}

func canonicalBundleHash(defs *bpmncore.BPMNDefinitions) (string, error) {
	procs := defs.Processes
	type procHash struct{ id, hash string }
	hashes := make([]procHash, len(procs))
	for i := range procs {
		h, err := canonicalHash(&procs[i])
		if err != nil {
			return "", fmt.Errorf("process %q: %w", procs[i].ID, err)
		}
		hashes[i] = procHash{id: procs[i].ID, hash: h}
	}
	ref := definitionReferences(defs)
	if len(procs) == 1 && ref.empty() {
		return hashes[0].hash, nil
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
	if !ref.empty() {
		data, err := json.Marshal(ref)
		if err != nil {
			return "", fmt.Errorf("canonical hash marshal references: %w", err)
		}
		bundle.Write(data)
	}
	return hex.EncodeToString(bundle.Sum(nil)), nil
}
