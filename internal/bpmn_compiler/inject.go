package bpmn_compiler

import (
	"context"
	"encoding/xml"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// bundleModules adds each resolved library module version to mainXML as a
// process named by its plan name, and points every library call at the plan
// it resolved to. Whether a call is a library call is decided in the document
// it was written in, so a module's call never takes over a local process of the
// same id. Module diagrams are dropped. Each module XML goes through the same
// security checks as the main BPMN.
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
		defs.Processes = append(defs.Processes, proc)
		bundled[m.PlanName] = true
	}
	out, err := xml.Marshal(defs)
	if err != nil {
		return "", fmt.Errorf("bundle: marshal bundled BPMN: %w", err)
	}
	return xml.Header + string(out), nil
}

// pointLibraryCalls points proc's library calls, as judged within its own
// document home, at their plans.
func pointLibraryCalls(proc *bpmncore.BPMNProcess, home *bpmncore.BPMNDefinitions, planFor map[moduleKey]string) {
	for j := range proc.CallActivities {
		call, _, ok := moduleCall(proc.CallActivities[j], home)
		if !ok {
			continue
		}
		if plan, found := planFor[moduleKey{call.ProcessID, call.VersionNumber}]; found {
			ext := proc.CallActivities[j].ExtensionElements.ZeebeCalledElement
			ext.ProcessID, ext.BindingType, ext.VersionTag = plan, "", ""
		}
	}
}

// moduleKey is a call target: a module's process id and the version pinned,
// or zero for its active version.
type moduleKey struct {
	processID string
	version   int32
}

// pointSelfCalls renames a module's calls to its own process along with it, so
// a module that calls itself is reported as the cycle it is.
func pointSelfCalls(proc *bpmncore.BPMNProcess, planName string) {
	for j := range proc.CallActivities {
		if ext := proc.CallActivities[j].ExtensionElements.ZeebeCalledElement; ext != nil && ext.ProcessID == proc.ID {
			ext.ProcessID, ext.BindingType, ext.VersionTag = planName, "", ""
		}
	}
}
