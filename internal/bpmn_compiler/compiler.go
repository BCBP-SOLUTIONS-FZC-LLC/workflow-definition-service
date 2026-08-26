package bpmn_compiler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/element"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type ElementHandler = bpmncore.ElementHandler

func classifyParseError(stage string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("bpmn %s: %w", stage, err)
	}
	if errors.Is(err, errForbiddenXML) {
		return fmt.Errorf("bpmn %s: %w: %w: %v", stage, domain.ErrMalformedBPMN, domain.ErrForbiddenXML, err)
	}
	return fmt.Errorf("bpmn %s: %w: %v", stage, domain.ErrMalformedBPMN, err)
}

var _ port.PlanCompiler = (*Compiler)(nil)

type Compiler struct {
	stageTypes map[string]bpmncore.StageTypeHandler
	elements   map[bpmncore.FlowNodeType]bpmncore.ElementHandler
}

type CompilerOption func(*Compiler)

func WithStageType(h StageTypeHandler) CompilerOption {
	return func(c *Compiler) { c.stageTypes[h.ID()] = h }
}

func WithElementHandler(h ElementHandler) CompilerOption {
	return func(c *Compiler) { c.elements[h.NodeType()] = h }
}

func NewCompiler(opts ...CompilerOption) *Compiler {
	c := &Compiler{
		stageTypes: defaultStageTypes(),
		elements:   element.DefaultElementHandlers(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func New() *Compiler { return NewCompiler() }

func (c *Compiler) Validate(ctx context.Context, bpmnXML string) ([]domain.BPMNValidationError, error) {
	defs, err := parse(ctx, bpmnXML)
	if errors.Is(err, errMissingNamespace) {
		return []domain.BPMNValidationError{{Code: domain.BPMNErrMissingNamespace, Message: err.Error(), Severity: domain.SeverityError}}, nil
	}
	if err != nil {
		return nil, classifyParseError("validate", err)
	}

	execProcs := executableProcesses(defs)
	if len(execProcs) != 1 && defs.Collaboration == nil {
		return []domain.BPMNValidationError{{
			Code:     domain.BPMNErrMultipleProcesses,
			Message:  fmt.Sprintf("definitions contains %d executable processes without a collaboration wrapper; use <bpmn:collaboration> for multi-pool BPMN", len(execProcs)),
			Severity: domain.SeverityError,
		}}, nil
	}

	rejected := scanRejected(bpmnXML)
	collabErrs := validator.ValidateCollaboration(defs.Collaboration, defs)

	if defs.Collaboration != nil {
		ignoredProcIDs := make(map[string]bool, len(defs.Processes))
		participantNameByProcID := make(map[string]string, len(defs.Collaboration.Participants))
		for _, p := range defs.Collaboration.Participants {
			participantNameByProcID[p.ProcessRef] = p.Name
		}
		for i := range defs.Processes {
			if isIgnoredProcess(&defs.Processes[i]) {
				ignoredProcIDs[defs.Processes[i].ID] = true
			}
		}
		var allErrs []domain.BPMNValidationError
		allErrs = append(allErrs, rejected...)
		allErrs = append(allErrs, collabErrs...)
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
		return allErrs, nil
	}

	proc := execProcs[0]
	g := bpmncore.BuildGraph(proc)
	allErrs := append(rejected, validator.Validate(proc, g, c.stageTypes, c.elements, defs)...)
	allErrs = append(allErrs, validator.ValidateDiagramCompleteness(proc, defs)...)
	return allErrs, nil
}

func (c *Compiler) Compile(ctx context.Context, bpmnXML string) (*dsl.CompiledPlan, error) {
	defs, err := parse(ctx, bpmnXML)
	if errors.Is(err, errMissingNamespace) {
		return nil, &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{
			Code: domain.BPMNErrMissingNamespace, Message: err.Error(), Severity: domain.SeverityError,
		}}}
	}
	if err != nil {
		return nil, classifyParseError("compile", err)
	}
	execProcs := executableProcesses(defs)
	if len(execProcs) != 1 {
		return nil, &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{
			Code:     domain.BPMNErrMultipleProcesses,
			Message:  fmt.Sprintf("Compile expects a single executable process; got %d — use CompileCollaboration for multi-pool BPMN", len(execProcs)),
			Severity: domain.SeverityError,
		}}}
	}
	proc := execProcs[0]
	g := bpmncore.BuildGraph(proc)

	errs := append(scanRejected(bpmnXML), validator.Validate(proc, g, c.stageTypes, c.elements, defs)...)
	errs = append(errs, validator.ValidateDiagramCompleteness(proc, defs)...)
	if hasBlockingErrors(errs) {
		return nil, &domain.ValidationFailedError{Errors: errs}
	}

	plan, err := bpmncore.Compile(proc, g, c.stageTypes, c.elements, defs)
	if err != nil {
		return nil, fmt.Errorf("bpmn compile: %w", err)
	}
	return plan, nil
}

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

// collaborationProcSets builds the set of ignored process IDs and a
// process-ref → participant-name lookup, used to classify main vs. ignored
// pools throughout collaboration compilation.
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

// validateCollaborationProcesses runs structural/semantic validation for every
// non-ignored process in the collaboration, injecting message bridges and
// resolving each process's implicit start first so validation sees the same
// graph shape compilation will later use.
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

// compileCollaborationPlans compiles every participant process into a
// CompiledPlan — ignored pools best-effort via compileIgnoredPool, others via
// the normal traversal with message bridges injected — and qualifies each
// plan's department IDs with its own plan name.
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

// buildMessageDefs assembles the collaboration-level message index linking
// plans by message name, resolved via ResolveMessageFlowName.
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

// resolveMainPlanName returns the process name of the primary pool — the one
// with a <zeebe:property name="main" value="1"/> — or falls back to the first
// non-ignored process. The returned value matches CompiledPlan.Name so consumers
// can locate the entry plan by name without needing participant metadata.
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

// compileIgnoredPool compiles an ignored process best-effort (no validation).
// If traversal fails the plan is returned with an empty execution so consumers
// can still see the pool in the output with Ignored: true.
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

// validateCallPoolConstraints checks that no ignored pool emits a call_pool step.
// Only the main pool is allowed to call other pools as child workflows.
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

// buildMessageBridges finds all message-flow crossings between a main process
// and any ignored participant pools and returns a MessageBridge for each.
// A round-trip bridge is created when a path exists in the ignored process from
// the task that receives the message to a task that sends a message back.
// A terminal bridge is created when the main process sends to the ignored pool
// with no corresponding return message flow.
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

// collectMainElementIDs returns the set of element IDs that belong to the
// main (non-ignored) process, used to classify message flow endpoints.
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

// collectIgnoredTaskProcs maps every element ID belonging to an ignored
// process to that process's ID.
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

// correlateMessageFlows classifies collaboration message flows by direction:
// mainToIgnored maps a main-process sender to the ignored process it targets;
// ignoredToMain maps an ignored-process sender to the main-process task it targets.
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

// buildIgnoredGraphs builds a Graph for each ignored process, used to find
// paths from an ignored-process receive point to its return send point.
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

// findReturnTaskID walks the ignored process forward from its receive task to
// find the first not-yet-used send task with a message flow back to the main
// process, marking it used so each return task is claimed by one bridge only.
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

// collabImplicitStart returns the entry-point node ID for a non-ignored process
// in a collaboration by following message flows FROM ignored processes.
// When the ignored pool's message flow targets a node in proc, that node is the
// implicit start (i.e. the process is triggered by that message, not a start event).
// Falls back to FindImplicitStart (graph topology) when no message flow is found.
func collabImplicitStart(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, defs *bpmncore.BPMNDefinitions, ignoredProcIDs map[string]bool) string {
	if len(proc.StartEvents) > 0 {
		return ""
	}
	if defs.Collaboration == nil {
		return bpmncore.FindImplicitStart(proc, g)
	}

	// Walk each ignored process from its start event in BFS order and return
	// the target of the FIRST message flow that lands in this process.
	// This gives us the primary entry triggered by the ignored pool's initial
	// actions rather than a loop-back message sent later in the flow.
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

// firstMessageTargetInProc BFS-walks the ignored process ignoredProc from its
// start event and returns the target node ID of the first message flow whose
// source is reachable there and whose target lands in proc's graph. When the
// message lands on a boundary event, the host task is returned instead — it
// must be running to receive the message, so it is the actual implicit root.
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

// executableProcesses returns the root entry-point processes to compile.
//
// isExecutable semantics:
//   - false → visual-only pool (e.g. a Client pool showing only message flows); skip entirely.
//   - true  → real executable process (both root entry-points AND module/called processes).
//
// Root vs. called is determined by whether the process ID appears as a zeebe:calledElement
// target in any other process — NOT by isExecutable. Called processes are compiled
// recursively when encountered via callActivity.
func executableProcesses(defs *bpmncore.BPMNDefinitions) []*bpmncore.BPMNProcess {
	if len(defs.Processes) == 1 {
		return []*bpmncore.BPMNProcess{&defs.Processes[0]}
	}
	calledIDs := map[string]bool{}
	for i := range defs.Processes {
		for _, ca := range defs.Processes[i].CallActivities {
			if ca.ExtensionElements.ZeebeCalledElement != nil && ca.ExtensionElements.ZeebeCalledElement.ProcessID != "" {
				calledIDs[ca.ExtensionElements.ZeebeCalledElement.ProcessID] = true
			}
		}
	}
	var out []*bpmncore.BPMNProcess
	for i := range defs.Processes {
		p := &defs.Processes[i]
		if p.IsExecutable && !calledIDs[p.ID] {
			out = append(out, p)
		}
	}
	return out
}

func isIgnoredProcess(proc *bpmncore.BPMNProcess) bool {
	for _, p := range proc.ExtensionElements.ZeebeProps.Items {
		if p.Name == "ignore" && p.Value == "true" {
			return true
		}
	}
	return false
}

// hasBlockingErrors returns true if any entry has error severity (or unset severity,
// for backwards compatibility with callers that predate the Severity field).
func hasBlockingErrors(errs []domain.BPMNValidationError) bool {
	for _, e := range errs {
		if e.Severity != domain.SeverityWarning {
			return true
		}
	}
	return false
}

func (c *Compiler) Hash(ctx context.Context, bpmnXML string) (string, error) {
	defs, err := parse(ctx, bpmnXML)
	if err != nil {
		return "", fmt.Errorf("bpmn hash: %w", err)
	}
	if len(defs.Processes) != 1 {
		return "", fmt.Errorf("bpmn hash: expected exactly one process, got %d", len(defs.Processes))
	}
	h, err := canonicalHash(&defs.Processes[0])
	if err != nil {
		return "", fmt.Errorf("bpmn hash: %w", err)
	}
	return h, nil
}

// Bundle merges module BPMNs into mainXML. Satisfies port.PlanCompiler.
func (c *Compiler) Bundle(mainXML string, moduleXMLs []string) (string, error) {
	return InjectTemplates(mainXML, moduleXMLs)
}

func (c *Compiler) ProcessID(ctx context.Context, bpmnXML string) (string, error) {
	defs, err := parse(ctx, bpmnXML)
	if err != nil {
		return "", classifyParseError("process-id", err)
	}
	if len(defs.Processes) != 1 {
		return "", &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{
			Code:     domain.BPMNErrMultipleProcesses,
			Message:  fmt.Sprintf("module BPMN must contain exactly one top-level process; got %d", len(defs.Processes)),
			Severity: domain.SeverityError,
		}}}
	}
	return defs.Processes[0].ID, nil
}
