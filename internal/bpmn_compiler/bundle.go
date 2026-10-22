package bpmn_compiler

import (
	"context"
	"encoding/xml"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func bundleModules(mainXML string, modules []domain.ResolvedModule) (string, error) {
	if len(modules) == 0 {
		return mainXML, nil
	}
	defs, err := unmarshal(mainXML)
	if err != nil {
		return "", fmt.Errorf("bundle: parse main BPMN: %w", err)
	}
	planFor := make(map[moduleKey]string, len(modules))
	for _, m := range modules {
		planFor[moduleKey{m.ProcessID, m.VersionNumber}] = m.PlanName
	}
	for i := range defs.Processes {
		pointLibraryCalls(&defs.Processes[i], defs, planFor)
	}
	bundled := map[string]bool{}
	for _, m := range modules {
		if bundled[m.PlanName] {
			continue
		}
		modDefs, err := parse(context.Background(), m.BPMNXML)
		if err != nil {
			return "", fmt.Errorf("bundle: parse module %s: %w", m.PlanName, err)
		}
		if len(modDefs.Processes) != 1 {
			return "", fmt.Errorf("bundle: module %s holds %d processes, want 1", m.PlanName, len(modDefs.Processes))
		}
		proc := modDefs.Processes[0]
		pointLibraryCalls(&proc, modDefs, planFor)
		pointSelfCalls(&proc, m.PlanName)
		proc.ID = m.PlanName
		bundleDefinitions(defs, modDefs, &proc, m.PlanName)
		defs.Processes = append(defs.Processes, proc)
		bundled[m.PlanName] = true
	}
	out, err := xml.Marshal(defs)
	if err != nil {
		return "", fmt.Errorf("bundle: marshal bundled BPMN: %w", err)
	}
	return xml.Header + string(out), nil
}

func bundleDefinitions(defs, modDefs *bpmncore.BPMNDefinitions, proc *bpmncore.BPMNProcess, planName string) {
	own := func(id string) string {
		if id == "" {
			return ""
		}
		return planName + "/" + id
	}
	for _, m := range modDefs.Messages {
		m.ID = own(m.ID)
		defs.Messages = append(defs.Messages, m)
	}
	for _, e := range modDefs.Errors {
		e.ID = own(e.ID)
		defs.Errors = append(defs.Errors, e)
	}
	renameRefs(proc.SendTasks, proc.ReceiveTasks, proc.BoundaryEvents, proc.SubProcesses, own)
}

func renameRefs(sends []bpmncore.BPMNSendTask, receives []bpmncore.BPMNReceiveTask, boundaries []bpmncore.BPMNBoundaryEvent, subs []bpmncore.BPMNSubProcess, own func(string) string) {
	for i := range sends {
		sends[i].MessageRef = own(sends[i].MessageRef)
	}
	for i := range receives {
		receives[i].MessageRef = own(receives[i].MessageRef)
	}
	for i := range boundaries {
		if m := boundaries[i].Message; m != nil {
			m.MessageRef = own(m.MessageRef)
		}
		if e := boundaries[i].Error; e != nil {
			e.ErrorRef = own(e.ErrorRef)
		}
	}
	for i := range subs {
		renameRefs(subs[i].SendTasks, subs[i].ReceiveTasks, subs[i].BoundaryEvents, subs[i].SubProcesses, own)
	}
}

func pointLibraryCalls(proc *bpmncore.BPMNProcess, home *bpmncore.BPMNDefinitions, planFor map[moduleKey]string) {
	for j := range proc.CallActivities {
		call, _, ok := moduleCall(proc.CallActivities[j], proc.ID, home)
		if !ok {
			continue
		}
		if plan, found := planFor[moduleKey{call.ProcessID, call.VersionNumber}]; found {
			ext := proc.CallActivities[j].ExtensionElements.ZeebeCalledElement
			ext.ProcessID, ext.BindingType, ext.VersionTag = plan, "", ""
		}
	}
}

type moduleKey struct {
	processID string
	version   int32
}

func pointSelfCalls(proc *bpmncore.BPMNProcess, planName string) {
	for j := range proc.CallActivities {
		if ext := proc.CallActivities[j].ExtensionElements.ZeebeCalledElement; ext != nil && ext.ProcessID == proc.ID {
			ext.ProcessID, ext.BindingType, ext.VersionTag = planName, "", ""
		}
	}
}
