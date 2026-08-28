package bpmn_compiler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func (c *Compiler) CompileCollaboration(ctx context.Context, bpmnXML string) (*dsl.CompiledCollaboration, error) {
	defs, err := parse(ctx, bpmnXML)
	if errors.Is(err, errMissingNamespace) {
		return nil, &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{
			Code: domain.BPMNErrMissingNamespace, Message: err.Error(), Severity: domain.SeverityError,
		}}}
	}
	if err != nil {
		return nil, classifyParseError("compile-collaboration", err)
	}
	if defs.Collaboration == nil {
		return nil, &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{
			Code:     domain.BPMNErrMultipleProcesses,
			Message:  "CompileCollaboration requires a <bpmn:collaboration> element; use Compile for single-process BPMN",
			Severity: domain.SeverityError,
		}}}
	}

	ignoredProcIDs, participantNameByProcID := collaborationProcSets(defs)

	valErrs := c.validateCollaborationProcesses(bpmnXML, defs, ignoredProcIDs, participantNameByProcID)
	if hasBlockingErrors(valErrs) {
		return nil, &domain.ValidationFailedError{Errors: valErrs}
	}

	mainPlan := resolveMainPlanName(defs.Processes)
	plans, err := c.compileCollaborationPlans(defs, ignoredProcIDs, participantNameByProcID)
	if err != nil {
		return nil, err
	}
	if err := validateCallPoolConstraints(plans, mainPlan); err != nil {
		return nil, err
	}

	return &dsl.CompiledCollaboration{
		MainPlan:      mainPlan,
		Plans:         plans,
		Messages:      buildMessageDefs(defs),
		SchemaVersion: dsl.CurrentSchemaVersion,
	}, nil
}

// validateCollaboration is Validate's collaboration path: unlike
// validateCollaborationProcesses (CompileCollaboration's pre-check), it also
// runs ValidateDiagramCompleteness per process, matching what the
// single-process path below it in Validate does.
func (c *Compiler) validateCollaboration(rejected []domain.BPMNValidationError, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	collabErrs := validator.ValidateCollaboration(defs.Collaboration, defs)
	ignoredProcIDs, participantNameByProcID := collaborationProcSets(defs)

	allErrs := append(rejected, collabErrs...)
	for i := range defs.Processes {
		proc := &defs.Processes[i]
		if isIgnoredProcess(proc) {
			continue
		}
		g := bpmncore.BuildGraph(proc)
		bridges := buildMessageBridges(proc, defs, ignoredProcIDs, participantNameByProcID)
		bpmncore.InjectMessageBridges(g, bridges)
		implicitStart := collabImplicitStart(proc, g, defs, ignoredProcIDs)
		procErrs := validator.ValidateWithImplicitStart(proc, g, implicitStart, c.stageTypes, c.elements, defs)
		if hasTerminalBridges(bridges) {
			procErrs = filterCode(procErrs)
		}
		allErrs = append(allErrs, procErrs...)
		allErrs = append(allErrs, validator.ValidateDiagramCompleteness(proc, defs)...)
	}
	return allErrs
}

func collaborationProcSets(defs *bpmncore.BPMNDefinitions) (ignoredProcIDs map[string]bool, participantNameByProcID map[string]string) {
	ignoredProcIDs = make(map[string]bool, len(defs.Processes))
	participantNameByProcID = make(map[string]string, len(defs.Collaboration.Participants))
	for _, p := range defs.Collaboration.Participants {
		participantNameByProcID[p.ProcessRef] = p.Name
	}
	for i := range defs.Processes {
		if isIgnoredProcess(&defs.Processes[i]) {
			ignoredProcIDs[defs.Processes[i].ID] = true
		}
	}
	return ignoredProcIDs, participantNameByProcID
}

func (c *Compiler) validateCollaborationProcesses(bpmnXML string, defs *bpmncore.BPMNDefinitions, ignoredProcIDs map[string]bool, participantNameByProcID map[string]string) []domain.BPMNValidationError {
	var valErrs []domain.BPMNValidationError
	valErrs = append(valErrs, scanRejected(bpmnXML)...)
	valErrs = append(valErrs, validator.ValidateCollaboration(defs.Collaboration, defs)...)
	for i := range defs.Processes {
		proc := &defs.Processes[i]
		if isIgnoredProcess(proc) {
			continue
		}
		g := bpmncore.BuildGraph(proc)
		bridges := buildMessageBridges(proc, defs, ignoredProcIDs, participantNameByProcID)
		bpmncore.InjectMessageBridges(g, bridges)
		implicitStart := collabImplicitStart(proc, g, defs, ignoredProcIDs)
		procErrs := validator.ValidateWithImplicitStart(proc, g, implicitStart, c.stageTypes, c.elements, defs)
		if hasTerminalBridges(bridges) {
			procErrs = filterCode(procErrs)
		}
		valErrs = append(valErrs, procErrs...)
	}
	return valErrs
}

func (c *Compiler) compileCollaborationPlans(defs *bpmncore.BPMNDefinitions, ignoredProcIDs map[string]bool, participantNameByProcID map[string]string) ([]*dsl.CompiledPlan, error) {
	procByID := make(map[string]*bpmncore.BPMNProcess, len(defs.Processes))
	for i := range defs.Processes {
		procByID[defs.Processes[i].ID] = &defs.Processes[i]
	}

	var plans []*dsl.CompiledPlan
	for _, participant := range defs.Collaboration.Participants {
		proc, ok := procByID[participant.ProcessRef]
		if !ok {
			continue
		}
		g := bpmncore.BuildGraph(proc)

		if isIgnoredProcess(proc) {
			plan := compileIgnoredPool(proc, g, c.stageTypes, c.elements, defs)
			bpmncore.QualifyPlanDepts(plan, plan.Name)
			plans = append(plans, plan)
			continue
		}

		bridges := buildMessageBridges(proc, defs, ignoredProcIDs, participantNameByProcID)
		bpmncore.InjectMessageBridges(g, bridges)
		extNodes := make(map[string]string, len(bridges))
		for _, b := range bridges {
			extNodes[b.SyntheticNodeID] = b.ParticipantName
		}
		implicitStart := collabImplicitStart(proc, g, defs, ignoredProcIDs)
		plan, err := bpmncore.CompileWithImplicitStart(proc, g, implicitStart, c.stageTypes, c.elements, defs, extNodes)
		if err != nil {
			return nil, fmt.Errorf("bpmn compile-collaboration participant %q: %w", participant.ID, err)
		}
		bpmncore.QualifyPlanDepts(plan, plan.Name)
		plans = append(plans, plan)
	}
	return plans, nil
}

func buildMessageDefs(defs *bpmncore.BPMNDefinitions) []dsl.MessageDef {
	elementToProcess := buildElementToProcessName(defs.Processes)

	var messages []dsl.MessageDef
	for i := range defs.Collaboration.MessageFlows {
		mf := &defs.Collaboration.MessageFlows[i]
		messages = append(messages, dsl.MessageDef{
			Name:       bpmncore.ResolveMessageFlowName(mf, defs),
			SourcePlan: elementToProcess[mf.SourceRef],
			TargetPlan: elementToProcess[mf.TargetRef],
		})
	}
	return messages
}

func resolveMainPlanName(procs []bpmncore.BPMNProcess) string {
	for i := range procs {
		for _, p := range procs[i].ExtensionElements.ZeebeProps.Items {
			if p.Name == "main" && p.Value == "1" {
				return procs[i].Name
			}
		}
	}
	for i := range procs {
		if !isIgnoredProcess(&procs[i]) {
			return procs[i].Name
		}
	}
	return ""
}

// compileIgnoredPool deliberately swallows a traversal error: an ignored pool
// still needs to appear in the output with Ignored: true even when it can't
// be compiled, so a failure yields an empty-execution plan rather than
// propagating.
func compileIgnoredPool(
	proc *bpmncore.BPMNProcess,
	g *bpmncore.Graph,
	stageTypes map[string]bpmncore.StageTypeHandler,
	elements map[bpmncore.FlowNodeType]bpmncore.ElementHandler,
	defs *bpmncore.BPMNDefinitions,
) *dsl.CompiledPlan {
	implicitStart := bpmncore.FindImplicitStart(proc, g)
	plan, err := bpmncore.CompileWithImplicitStart(proc, g, implicitStart, stageTypes, elements, defs)
	if err != nil {
		plan = &dsl.CompiledPlan{Name: proc.Name}
	}
	plan.Ignored = true
	plan.TaskQueue = ""
	return plan
}

// validateCallPoolConstraints rejects a call_pool step from any pool other
// than the main one — only the main pool may call other pools as child workflows.
func validateCallPoolConstraints(plans []*dsl.CompiledPlan, mainPlan string) error {
	for _, plan := range plans {
		if !plan.Ignored || plan.Name == mainPlan {
			continue
		}
		if stepsHaveCallPool(plan.Execution.Steps) {
			return &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{
				Code:    domain.BPMNErrUnmatchedMessageFlow,
				Message: fmt.Sprintf("ignored pool %q contains a call_pool step; only the main pool may call other pools", plan.Name),
			}}}
		}
	}
	return nil
}

func stepsHaveCallPool(steps []dsl.ExecutionStep) bool {
	for i := range steps {
		if steps[i].CallPool != nil {
			return true
		}
		for j := range steps[i].Parallel {
			if stepsHaveCallPool(steps[i].Parallel[j].Steps) {
				return true
			}
		}
		if sw := steps[i].SubWorkflow; sw != nil {
			if stepsHaveCallPool(sw.Plan.Steps) {
				return true
			}
		}
	}
	return false
}

// buildMessageBridges returns a MessageBridge for every message-flow crossing
// between the main process and an ignored participant pool — see the
// MessageBridge field docs for the round-trip vs. terminal distinction.
func buildMessageBridges(mainProc *bpmncore.BPMNProcess, defs *bpmncore.BPMNDefinitions, ignoredProcIDs map[string]bool, participantNameByProcID map[string]string) []bpmncore.MessageBridge {
	if defs.Collaboration == nil {
		return nil
	}

	mainElems := collectMainElementIDs(mainProc)
	ignoredTaskProc := collectIgnoredTaskProcs(defs, ignoredProcIDs)
	mainToIgnored, ignoredToMain := correlateMessageFlows(defs, mainElems, ignoredTaskProc)
	ignoredGraphs := buildIgnoredGraphs(defs, ignoredProcIDs)

	var bridges []bpmncore.MessageBridge
	used := make(map[string]bool)

	for mainSendID, ignoredProcID := range mainToIgnored {
		var ignoredRecvID string
		for _, mf := range defs.Collaboration.MessageFlows {
			if mf.SourceRef == mainSendID {
				ignoredRecvID = mf.TargetRef
				break
			}
		}

		ig := ignoredGraphs[ignoredProcID]
		participantName := participantNameByProcID[ignoredProcID]
		h := sha256.Sum256([]byte(mainSendID))
		syntheticID := "__ext__" + hex.EncodeToString(h[:4])

		returnTaskID := findReturnTaskID(ig, ignoredRecvID, ignoredToMain, used)

		bridges = append(bridges, bpmncore.MessageBridge{
			FromTaskID:      mainSendID,
			ToTaskID:        returnTaskID,
			ParticipantName: participantName,
			SyntheticNodeID: syntheticID,
		})
	}
	return bridges
}

func collectMainElementIDs(mainProc *bpmncore.BPMNProcess) map[string]bool {
	mainElems := make(map[string]bool)
	for _, t := range mainProc.UserTasks {
		mainElems[t.ID] = true
	}
	for _, t := range mainProc.SendTasks {
		mainElems[t.ID] = true
	}
	for _, t := range mainProc.ReceiveTasks {
		mainElems[t.ID] = true
	}
	for _, e := range mainProc.StartEvents {
		mainElems[e.ID] = true
	}
	for _, e := range mainProc.EndEvents {
		mainElems[e.ID] = true
	}
	for _, gw := range mainProc.ExclusiveGateways {
		mainElems[gw.ID] = true
	}
	for _, sp := range mainProc.SubProcesses {
		mainElems[sp.ID] = true
	}
	for _, ca := range mainProc.CallActivities {
		mainElems[ca.ID] = true
	}
	return mainElems
}

func collectIgnoredTaskProcs(defs *bpmncore.BPMNDefinitions, ignoredProcIDs map[string]bool) map[string]string {
	ignoredTaskProc := make(map[string]string)
	for i := range defs.Processes {
		p := &defs.Processes[i]
		if !ignoredProcIDs[p.ID] {
			continue
		}
		collect := func(id string) { ignoredTaskProc[id] = p.ID }
		for _, t := range p.UserTasks {
			collect(t.ID)
		}
		for _, t := range p.SendTasks {
			collect(t.ID)
		}
		for _, t := range p.ReceiveTasks {
			collect(t.ID)
		}
		for _, e := range p.StartEvents {
			collect(e.ID)
		}
		for _, e := range p.EndEvents {
			collect(e.ID)
		}
		for _, ca := range p.CallActivities {
			collect(ca.ID)
		}
	}
	return ignoredTaskProc
}

func correlateMessageFlows(defs *bpmncore.BPMNDefinitions, mainElems map[string]bool, ignoredTaskProc map[string]string) (mainToIgnored, ignoredToMain map[string]string) {
	mainToIgnored = make(map[string]string)
	ignoredToMain = make(map[string]string)
	for _, mf := range defs.Collaboration.MessageFlows {
		if mainElems[mf.SourceRef] && ignoredTaskProc[mf.TargetRef] != "" {
			mainToIgnored[mf.SourceRef] = ignoredTaskProc[mf.TargetRef]
		}
		if ignoredTaskProc[mf.SourceRef] != "" && mainElems[mf.TargetRef] {
			ignoredToMain[mf.SourceRef] = mf.TargetRef
		}
	}
	return mainToIgnored, ignoredToMain
}

func buildIgnoredGraphs(defs *bpmncore.BPMNDefinitions, ignoredProcIDs map[string]bool) map[string]*bpmncore.Graph {
	ignoredGraphs := make(map[string]*bpmncore.Graph)
	for i := range defs.Processes {
		p := &defs.Processes[i]
		if ignoredProcIDs[p.ID] {
			ignoredGraphs[p.ID] = bpmncore.BuildGraph(p)
		}
	}
	return ignoredGraphs
}

// findReturnTaskID marks its result used so each return task is claimed by
// exactly one bridge, even when several bridges could reach the same task.
func findReturnTaskID(ig *bpmncore.Graph, ignoredRecvID string, ignoredToMain map[string]string, used map[string]bool) string {
	if ig == nil {
		return ""
	}
	visited := make(map[string]bool)
	queue := []string{ignoredRecvID}
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if visited[curr] {
			continue
		}
		visited[curr] = true
		if toMain, ok := ignoredToMain[curr]; ok && !used[toMain] {
			used[toMain] = true
			return toMain
		}
		for _, next := range ig.Outgoing[curr] {
			if !visited[next] {
				queue = append(queue, next)
			}
		}
	}
	return ""
}

func buildElementToProcessName(processes []bpmncore.BPMNProcess) map[string]string {
	m := make(map[string]string)
	for i := range processes {
		p := &processes[i]
		for _, t := range p.UserTasks {
			m[t.ID] = p.Name
		}
		for _, t := range p.SendTasks {
			m[t.ID] = p.Name
		}
		for _, t := range p.ReceiveTasks {
			m[t.ID] = p.Name
		}
		for _, e := range p.StartEvents {
			m[e.ID] = p.Name
		}
		for _, e := range p.EndEvents {
			m[e.ID] = p.Name
		}
		for _, g := range p.ParallelGateways {
			m[g.ID] = p.Name
		}
		for _, g := range p.ExclusiveGateways {
			m[g.ID] = p.Name
		}
		for _, g := range p.InclusiveGateways {
			m[g.ID] = p.Name
		}
		for _, sp := range p.SubProcesses {
			m[sp.ID] = p.Name
		}
		for _, ca := range p.CallActivities {
			m[ca.ID] = p.Name
		}
		for _, be := range p.BoundaryEvents {
			m[be.ID] = p.Name
		}
	}
	return m
}

// collabImplicitStart resolves a start-eventless process's entry point to
// wherever an ignored pool's message flow first lands it, falling back to
// FindImplicitStart's graph-topology heuristic when no such flow exists.
func collabImplicitStart(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, defs *bpmncore.BPMNDefinitions, ignoredProcIDs map[string]bool) string {
	if len(proc.StartEvents) > 0 {
		return ""
	}
	if defs.Collaboration == nil {
		return bpmncore.FindImplicitStart(proc, g)
	}

	// BFS from each ignored process's own start event and stop at its first
	// landing message flow: a later one risks being a loop-back reply, not
	// the initial trigger into proc.
	for i := range defs.Processes {
		p := &defs.Processes[i]
		if !ignoredProcIDs[p.ID] || len(p.StartEvents) == 0 {
			continue
		}
		if target := firstMessageTargetInProc(p, proc, g, defs); target != "" {
			return target
		}
	}

	return bpmncore.FindImplicitStart(proc, g)
}

// firstMessageTargetInProc BFS-walks ignoredProc from its start event to find
// the first message flow landing in proc. A boundary-event target resolves
// to its host task instead — the host must already be running to receive it,
// so the host is the real implicit root.
func firstMessageTargetInProc(ignoredProc, proc *bpmncore.BPMNProcess, g *bpmncore.Graph, defs *bpmncore.BPMNDefinitions) string {
	ig := bpmncore.BuildGraph(ignoredProc)
	visited := make(map[string]bool)
	queue := []string{ignoredProc.StartEvents[0].ID}
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if visited[curr] {
			continue
		}
		visited[curr] = true
		for _, mf := range defs.Collaboration.MessageFlows {
			if mf.SourceRef != curr {
				continue
			}
			if _, inProc := g.NodeIDs[mf.TargetRef]; !inProc {
				continue
			}
			if bpmncore.IsBoundaryEventType(g.NodeType[mf.TargetRef]) {
				for _, be := range proc.BoundaryEvents {
					if be.ID == mf.TargetRef {
						return be.AttachedToRef
					}
				}
			}
			return mf.TargetRef
		}
		for _, next := range ig.Outgoing[curr] {
			if !visited[next] {
				queue = append(queue, next)
			}
		}
	}
	return ""
}

func hasTerminalBridges(bridges []bpmncore.MessageBridge) bool {
	for _, b := range bridges {
		if b.ToTaskID == "" {
			return true
		}
	}
	return false
}

func filterCode(errs []domain.BPMNValidationError) []domain.BPMNValidationError {
	out := make([]domain.BPMNValidationError, 0, len(errs))
	for _, e := range errs {
		if e.Code != domain.BPMNErrNoEndEvent {
			out = append(out, e)
		}
	}
	return out
}
