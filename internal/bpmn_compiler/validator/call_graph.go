package validator

import (
	"fmt"
	"slices"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// ValidateCallGraph reports process ids that occur more than once, and call
// chains that return to a process already on them. Called processes are
// flattened into the caller's plan, so a cycle has no finite plan.
func ValidateCallGraph(defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	errs := validateUniqueProcessIDs(defs)

	const (
		unvisited = iota
		onChain
		done
	)
	state := make(map[string]int, len(defs.Processes))
	var chain []string
	var visit func(p *bpmncore.BPMNProcess)
	visit = func(p *bpmncore.BPMNProcess) {
		state[p.ID] = onChain
		chain = append(chain, p.ID)
		for _, ca := range p.CallActivities {
			called := calledProcess(ca, defs)
			if called == nil {
				continue
			}
			switch state[called.ID] {
			case onChain:
				cycle := chain[slices.Index(chain, called.ID):]
				errs = AppendErr(errs, domain.BPMNErrCalledElementCycle, ca.ID,
					fmt.Sprintf("callActivity closes the call cycle %s → %s", strings.Join(cycle, " → "), called.ID))
			case unvisited:
				visit(called)
			}
		}
		chain = chain[:len(chain)-1]
		state[p.ID] = done
	}
	for i := range defs.Processes {
		if state[defs.Processes[i].ID] == unvisited {
			visit(&defs.Processes[i])
		}
	}
	return errs
}

func validateUniqueProcessIDs(defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	count := make(map[string]int, len(defs.Processes))
	for _, p := range defs.Processes {
		count[p.ID]++
		if count[p.ID] == 2 {
			errs = AppendErr(errs, domain.BPMNErrDuplicateProcessID, p.ID,
				fmt.Sprintf("more than one bpmn:process has id %q; check module_bpmn_xmls for a repeated or clashing module", p.ID))
		}
	}
	return errs
}

// ValidateCalledBodies validates, once each, the processes root reaches through
// callActivities. It skips the diagram rules, since Bundle drops module
// diagrams, and any body with no start event, which its call site reports.
func ValidateCalledBodies(root *bpmncore.BPMNProcess, stageTypes map[string]bpmncore.StageTypeHandler, elements map[bpmncore.FlowNodeType]bpmncore.ElementHandler, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	visited := map[string]bool{root.ID: true}
	queue := []*bpmncore.BPMNProcess{root}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, ca := range p.CallActivities {
			called := calledProcess(ca, defs)
			if called == nil || visited[called.ID] || len(called.StartEvents) == 0 {
				continue
			}
			visited[called.ID] = true
			errs = append(errs, AnchorOn(called.ID, Validate(called, bpmncore.BuildGraph(called), stageTypes, elements, defs))...)
			queue = append(queue, called)
		}
	}
	return errs
}

// AnchorOn gives errors about a whole nested process, which name no node, the
// id of the element that holds that process: a called process or a subProcess.
func AnchorOn(nodeID string, errs []domain.BPMNValidationError) []domain.BPMNValidationError {
	for i := range errs {
		if errs[i].NodeID == "" {
			errs[i].NodeID = nodeID
		}
	}
	return errs
}

func calledProcess(ca bpmncore.BPMNCallActivity, defs *bpmncore.BPMNDefinitions) *bpmncore.BPMNProcess {
	ext := ca.ExtensionElements.ZeebeCalledElement
	if ext == nil || ext.ProcessID == "" {
		return nil
	}
	return bpmncore.FindProcessByID(defs, ext.ProcessID)
}
