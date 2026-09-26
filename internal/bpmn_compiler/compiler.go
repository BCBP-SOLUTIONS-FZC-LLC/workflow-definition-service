package bpmn_compiler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/enums"

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

	callGraphErrs := validator.ValidateCallGraph(defs)
	execProcs := executableProcesses(defs)
	if len(execProcs) != 1 && defs.Collaboration == nil {
		return append(callGraphErrs, domain.BPMNValidationError{
			Code:     domain.BPMNErrMultipleProcesses,
			Message:  fmt.Sprintf("definitions contains %d executable processes without a collaboration wrapper; use <bpmn:collaboration> for multi-pool BPMN", len(execProcs)),
			Severity: domain.SeverityError,
		}), nil
	}

	if defs.Collaboration != nil {
		return c.validateCollaboration(append(scanRejected(bpmnXML), callGraphErrs...), defs), nil
	}
	return append(callGraphErrs, c.validateSingleRoot(bpmnXML, execProcs[0], defs)...), nil
}

// ValidateForPublish is Validate plus what only the compiled plan, with its
// calls expanded, shows and publish refuses: the task limit and a task no one
// is named for. A document with a blocking error, or a collaboration, which
// publish refuses outright, gets Validate's answer alone.
func (c *Compiler) ValidateForPublish(ctx context.Context, bpmnXML string) ([]domain.BPMNValidationError, error) {
	errs, err := c.Validate(ctx, bpmnXML)
	if err != nil || slices.ContainsFunc(errs, isBlocking) {
		return errs, err
	}
	if defs, err := parse(ctx, bpmnXML); err != nil || defs.Collaboration != nil {
		return errs, nil
	}
	_, err = c.CompileForPublish(ctx, bpmnXML)
	var failed *domain.ValidationFailedError
	switch {
	case errors.As(err, &failed):
		return append(withoutCode(errs, domain.BPMNErrCandidateUsersEmpty), failed.Errors...), nil
	case err != nil:
		return nil, err
	}
	return withoutCode(errs, domain.BPMNErrCandidateUsersEmpty), nil
}

func isBlocking(e domain.BPMNValidationError) bool { return e.Severity != domain.SeverityWarning }

// withoutCode drops code's warnings: the expanded plan's own check replaces
// the per-document CANDIDATE_USERS_EMPTY warning once a workflow is judged
// for publish, since a call may name the user the module leaves out.
func withoutCode(errs []domain.BPMNValidationError, code domain.BPMNErrorCode) []domain.BPMNValidationError {
	return slices.DeleteFunc(errs, func(e domain.BPMNValidationError) bool { return e.Code == code && !isBlocking(e) })
}

// validateSingleRoot runs every check a single-root BPMN must pass apart from
// ValidateCallGraph, which callers run first because it also explains a
// MULTIPLE_PROCESSES caused by a duplicated process id.
func (c *Compiler) validateSingleRoot(bpmnXML string, proc *bpmncore.BPMNProcess, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	g := bpmncore.BuildGraph(proc)
	errs := append(scanRejected(bpmnXML), validator.Validate(proc, g, c.stageTypes, c.elements, defs)...)
	errs = append(errs, validator.ValidateCalledBodies(proc, c.stageTypes, c.elements, defs)...)
	return append(errs, validator.ValidateDiagramCompleteness(proc, defs)...)
}

func (c *Compiler) Compile(ctx context.Context, bpmnXML string) (*dsl.CompiledPlan, error) {
	plan, _, _, err := c.compileSingleRoot(ctx, bpmnXML)
	return plan, err
}

// CompileForPublish wraps a single root as a one-plan collaboration, the
// shape the Execution Service decodes. A collaboration BPMN is refused by
// compileSingleRoot until an ignored pool's call-pool task can be completed.
func (c *Compiler) CompileForPublish(ctx context.Context, bpmnXML string) (*dsl.CompiledCollaboration, error) {
	plan, called, root, err := c.compileSingleRoot(ctx, bpmnXML)
	if err != nil {
		return nil, err
	}
	if plan.Name == "" {
		plan.Name = root.ID
	}
	collab := &dsl.CompiledCollaboration{
		MainPlan:      plan.Name,
		Plans:         append([]*dsl.CompiledPlan{plan}, called...),
		Messages:      []dsl.MessageDef{},
		SchemaVersion: dsl.CurrentSchemaVersion,
	}
	if errs := unplacedTasks(plan); len(errs) > 0 {
		return nil, &domain.ValidationFailedError{Errors: errs}
	}
	if err := checkExpanded(collab, root.ID); err != nil {
		return nil, err
	}
	return collab, nil
}

// unplacedTasks reports each human task of the workflow's own plan that runs
// in no IAM department, as in a workflow without lanes: nobody could be
// checked eligible for it (code 35). A called plan's open departments take
// their IAM department from the call, so only the root's own are judged.
func unplacedTasks(plan *dsl.CompiledPlan) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for _, d := range plan.Departments {
		if d.Ignore || d.IAMDepartmentID != "" {
			continue
		}
		for _, s := range d.Stages {
			if createsHumanTask(s) {
				errs = append(errs, domain.BPMNValidationError{
					Code: domain.BPMNErrMissingDeptID, NodeID: s.NodeID, Severity: domain.SeverityError,
					Message: "task is in no lane with a dept_id, so it has no IAM department; put it in a lane whose dept_id names one",
				})
			}
		}
	}
	return errs
}

// checkExpanded expands collab's calls the way both services will, and
// refuses a plan whose stages and calls exceed the task limit (definition LLD
// §3.1.4 code 9) or that has a human task with no default user (code 8).
func checkExpanded(collab *dsl.CompiledCollaboration, rootID string) error {
	expanded, err := dsl.ExpandCalls(collab, validator.MaxUserTasks)
	if errors.Is(err, dsl.ErrPlanTooLarge) {
		return &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{
			Code:     domain.BPMNErrTaskLimitExceeded,
			NodeID:   rootID,
			Message:  fmt.Sprintf("process exceeds the %d task limit once its calls are expanded, counting each call and every stage it brings in", validator.MaxUserTasks),
			Severity: domain.SeverityError,
		}}}
	}
	if err != nil {
		return fmt.Errorf("bpmn compile: expand calls: %w", err)
	}
	if errs := unassignedTasks(expanded.Plans[0]); len(errs) > 0 {
		return &domain.ValidationFailedError{Errors: errs}
	}
	return nil
}

// unassignedTasks reports each human task of the expanded main plan that
// names no default user, on the element its author can fix: the task itself,
// or, for a task inside a module, the top-level call that runs it.
func unassignedTasks(plan *dsl.CompiledPlan) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for _, d := range plan.Departments {
		if d.Ignore {
			continue
		}
		for _, s := range d.Stages {
			if !createsHumanTask(s) || len(s.DefaultAssignees) > 0 {
				continue
			}
			node := s.NodeID
			if call, _, inModule := strings.Cut(d.ID, dsl.CallScopeSeparator); inModule {
				node = call
			}
			errs = append(errs, domain.BPMNValidationError{
				Code:   domain.BPMNErrCandidateUsersEmpty,
				NodeID: node,
				Message: fmt.Sprintf("task %s/%s names no default user; name one in its candidateUsers or in the Assignees of a call that runs it, "+
					"since a published workflow gives every task a default", d.ID, s.NodeID),
				Severity: domain.SeverityError,
			})
		}
	}
	return errs
}

// createsHumanTask matches the Execution Service's rule for which stages
// become tasks a person completes (execution LLD §5.5).
func createsHumanTask(s dsl.StageDef) bool {
	return s.ConnectorType == "" && s.Type != string(enums.StageTypeSendTask) && s.Type != string(enums.StageTypeReceiveTask)
}

func (c *Compiler) compileSingleRoot(ctx context.Context, bpmnXML string) (plan *dsl.CompiledPlan, called []*dsl.CompiledPlan, root *bpmncore.BPMNProcess, err error) {
	defs, err := parse(ctx, bpmnXML)
	if errors.Is(err, errMissingNamespace) {
		return nil, nil, nil, &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{
			Code: domain.BPMNErrMissingNamespace, Message: err.Error(), Severity: domain.SeverityError,
		}}}
	}
	if err != nil {
		return nil, nil, nil, classifyParseError("compile", err)
	}
	callGraphErrs := validator.ValidateCallGraph(defs)
	execProcs := executableProcesses(defs)
	if len(execProcs) != 1 {
		return nil, nil, nil, &domain.ValidationFailedError{Errors: append(callGraphErrs, domain.BPMNValidationError{
			Code:     domain.BPMNErrMultipleProcesses,
			Message:  fmt.Sprintf("Compile expects a single executable process; got %d — use CompileCollaboration for multi-pool BPMN", len(execProcs)),
			Severity: domain.SeverityError,
		})}
	}
	root = execProcs[0]

	errs := append(callGraphErrs, c.validateSingleRoot(bpmnXML, root, defs)...)
	if hasBlockingErrors(errs) {
		return nil, nil, nil, &domain.ValidationFailedError{Errors: errs}
	}

	plan, called, err = bpmncore.Compile(root, bpmncore.BuildGraph(root), c.stageTypes, c.elements, defs)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("bpmn compile: %w", err)
	}
	return plan, called, root, nil
}

// executableProcesses returns the root entry-point processes to compile — see
// .claude/bpmn-compiler.md § isExecutable Flag for the isExecutable and
// root-vs-called-process rules this applies.
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

// hasBlockingErrors treats an unset Severity as blocking too, for callers
// that predate the Severity field.
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
	if len(defs.Processes) == 0 {
		return "", errors.New("bpmn hash: no process")
	}
	h, err := canonicalBundleHash(defs.Processes)
	if err != nil {
		return "", fmt.Errorf("bpmn hash: %w", err)
	}
	return h, nil
}

func (c *Compiler) BundleModules(mainXML string, modules []domain.ResolvedModule) (string, error) {
	return bundleModules(mainXML, modules)
}

func (c *Compiler) ProcessID(ctx context.Context, bpmnXML string) (string, error) {
	defs, err := parse(ctx, bpmnXML)
	if errors.Is(err, errMissingNamespace) {
		return "", &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{
			Code: domain.BPMNErrMissingNamespace, Message: err.Error(), Severity: domain.SeverityError,
		}}}
	}
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
