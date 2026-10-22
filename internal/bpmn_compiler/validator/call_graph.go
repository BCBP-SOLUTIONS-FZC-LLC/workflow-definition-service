package validator

import (
	"fmt"
	"slices"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func ValidateCallGraph(defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	errs := validateUniqueProcessIDs(defs)

	const (
		unvisited = iota
		onChain
		done
	)
	state := make(map[string]int, len(defs.Processes))
	expandedSize := make(map[string]int, len(defs.Processes))
	isCalled := make(map[string]bool, len(defs.Processes))
	calls := make(map[string]bool, len(defs.Processes))
	var chain []string
	var visit func(p *bpmncore.BPMNProcess)
	visit = func(p *bpmncore.BPMNProcess) {
		state[p.ID] = onChain
		chain = append(chain, p.ID)
		size := len(p.UserTasks)
		for _, ca := range p.CallActivities {
			called := calledProcess(ca, defs)
			if called == nil || bpmncore.IsIgnored(p) {
				continue
			}
			isCalled[called.ID] = true
			calls[p.ID] = true
			switch state[called.ID] {
			case onChain:
				cycle := chain[slices.Index(chain, called.ID):]
				errs = AppendErr(errs, domain.BPMNErrCalledElementCycle, ca.ID,
					fmt.Sprintf("callActivity closes the call cycle %s → %s", strings.Join(cycle, " → "), called.ID))
			case unvisited:
				visit(called)
			}
			size = min(size+1+expandedSize[called.ID], maxUserTasks+1)
		}
		expandedSize[p.ID] = size
		chain = chain[:len(chain)-1]
		state[p.ID] = done
	}
	for i := range defs.Processes {
		if state[defs.Processes[i].ID] == unvisited {
			visit(&defs.Processes[i])
		}
	}
	errs = append(errs, validatePlanNameClash(defs, isCalled)...)
	return append(errs, validateExpandedSize(defs, expandedSize, isCalled, calls)...)
}

func validateExpandedSize(defs *bpmncore.BPMNDefinitions, expandedSize map[string]int, isCalled, calls map[string]bool) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for _, p := range defs.Processes {
		if isCalled[p.ID] || expandedSize[p.ID] <= maxUserTasks || !calls[p.ID] {
			continue
		}
		errs = AppendErr(errs, domain.BPMNErrTaskLimitExceeded, p.ID,
			fmt.Sprintf("process exceeds the %d task limit once its calls are expanded, counting each call", maxUserTasks))
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
				fmt.Sprintf("more than one bpmn:process in this file has id %q; give each process its own id", p.ID))
		}
	}
	return errs
}

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

func validatePlanNameClash(defs *bpmncore.BPMNDefinitions, isCalled map[string]bool) []domain.BPMNValidationError {
	ids := make(map[string]bool, len(defs.Processes))
	for _, p := range defs.Processes {
		ids[p.ID] = true
	}
	var errs []domain.BPMNValidationError
	for _, p := range defs.Processes {
		if isCalled[p.ID] || p.Name == "" || p.Name == p.ID || !ids[p.Name] {
			continue
		}
		errs = AppendErr(errs, domain.BPMNErrDuplicateProcessID, p.ID,
			fmt.Sprintf("the main process is named %q, which is also the id of another process in this file; a plan is named by the process name for the main process and by the id for a called one, so rename one of them", p.Name))
	}
	return errs
}
