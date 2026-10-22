package bpmn_compiler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

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
	root, rootErr := runnableRoot(defs)
	if rootErr != nil {
		return append(callGraphErrs, *rootErr), nil //nolint:nilerr // a root that cannot run is reported as a validation issue, not as a failure of the call
	}
	return slices.Concat(callGraphErrs, c.validateSingleRoot(bpmnXML, root, defs)), nil
}

func (c *Compiler) ValidateForPublish(ctx context.Context, bpmnXML string) ([]domain.BPMNValidationError, error) {
	errs, err := c.Validate(ctx, bpmnXML)
	if err != nil || slices.ContainsFunc(errs, isBlocking) {
		return errs, err
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

func withoutCode(errs []domain.BPMNValidationError, code domain.BPMNErrorCode) []domain.BPMNValidationError {
	return slices.DeleteFunc(errs, func(e domain.BPMNValidationError) bool { return e.Code == code && !isBlocking(e) })
}

func (c *Compiler) validateSingleRoot(bpmnXML string, proc *bpmncore.BPMNProcess, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	g := bpmncore.BuildGraph(proc)
	implicitStart := ""
	if len(proc.StartEvents) == 0 && defs.Collaboration != nil {
		implicitStart = bpmncore.FindImplicitStart(proc, g)
	}
	errs := append(scanRejected(bpmnXML), validator.ValidateWithImplicitStart(proc, g, implicitStart, c.stageTypes, c.elements, defs)...)
	errs = append(errs, validator.ValidateCollaboration(defs.Collaboration, defs, proc)...)
	errs = append(errs, validator.ValidateCalledBodies(proc, c.stageTypes, c.elements, defs)...)
	return append(errs, validator.ValidateDiagramCompleteness(proc, defs)...)
}

func (c *Compiler) Compile(ctx context.Context, bpmnXML string) (*dsl.CompiledPlan, error) {
	plan, _, _, err := c.compileSingleRoot(ctx, bpmnXML)
	return plan, err
}

func (c *Compiler) CompileForPublish(ctx context.Context, bpmnXML string) (*dsl.CompiledCollaboration, error) {
	collab, root, err := c.compileCollaboration(ctx, bpmnXML)
	if err != nil {
		return nil, err
	}
	if errs := lanelessWork(root); len(errs) > 0 {
		return nil, &domain.ValidationFailedError{Errors: errs}
	}
	if err := checkExpanded(collab, root.ID); err != nil {
		return nil, err
	}
	return collab, nil
}

func (c *Compiler) CompileDraft(ctx context.Context, bpmnXML string) (*dsl.CompiledCollaboration, error) {
	collab, _, err := c.compileCollaboration(ctx, bpmnXML)
	return collab, err
}

func (c *Compiler) compileCollaboration(ctx context.Context, bpmnXML string) (*dsl.CompiledCollaboration, *bpmncore.BPMNProcess, error) {
	plan, called, root, err := c.compileSingleRoot(ctx, bpmnXML)
	if err != nil {
		return nil, nil, err
	}
	if plan.Name == "" {
		plan.Name = root.ID
	}
	return &dsl.CompiledCollaboration{
		MainPlan:      plan.Name,
		Plans:         append([]*dsl.CompiledPlan{plan}, called...),
		SchemaVersion: dsl.CurrentSchemaVersion,
	}, root, nil
}

func lanelessWork(root *bpmncore.BPMNProcess) []domain.BPMNValidationError {
	if len(root.LaneSet.Lanes) > 0 {
		return nil
	}
	var ids []string
	for _, t := range root.UserTasks {
		ids = append(ids, t.ID)
	}
	for _, t := range root.SendTasks {
		ids = append(ids, t.ID)
	}
	for _, t := range root.ReceiveTasks {
		ids = append(ids, t.ID)
	}
	for _, c := range root.CallActivities {
		ids = append(ids, c.ID)
	}
	for _, sp := range root.SubProcesses {
		ids = append(ids, sp.ID)
	}
	errs := make([]domain.BPMNValidationError, 0, len(ids))
	for _, id := range ids {
		errs = append(errs, domain.BPMNValidationError{
			Code: domain.BPMNErrMissingDeptID, NodeID: id, Severity: domain.SeverityError,
			Message: "the workflow has no lanes, so this work runs in no IAM department; put it in a lane whose dept_id names one",
		})
	}
	return errs
}

func unplacedTasks(plan *dsl.CompiledPlan) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for _, d := range plan.Departments {
		if d.IAMDepartmentID != "" {
			continue
		}
		for _, s := range d.Stages {
			if s.CreatesHumanTask() {
				errs = append(errs, domain.BPMNValidationError{
					Code: domain.BPMNErrMissingDeptID, NodeID: authoredNode(d, s), Severity: domain.SeverityError,
					Message: fmt.Sprintf("task %s/%s runs in no IAM department; put it, or the call that runs it, in a lane whose dept_id names one", d.ID, s.NodeID),
				})
			}
		}
	}
	return errs
}

func checkExpanded(collab *dsl.CompiledCollaboration, rootID string) error {
	expanded, err := expand(collab, rootID)
	if err != nil {
		return err
	}
	main := expanded.Plans[0]
	if errs := append(unplacedTasks(main), unassignedTasks(main)...); len(errs) > 0 {
		return &domain.ValidationFailedError{Errors: errs}
	}
	return nil
}

func compileFailure(op, rootID string, err error) error {
	if errors.Is(err, bpmncore.ErrStageBudget) {
		return &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{
			Code:     domain.BPMNErrTaskLimitExceeded,
			NodeID:   rootID,
			Message:  fmt.Sprintf("process compiles to more than the %d task limit allows: branches that merge only in part are compiled once per branch", bpmncore.StageBudget),
			Severity: domain.SeverityError,
		}}}
	}
	return fmt.Errorf("%s: %w", op, err)
}

func expand(collab *dsl.CompiledCollaboration, rootID string) (*dsl.CompiledCollaboration, error) {
	expanded, err := dsl.ExpandCalls(collab, validator.MaxUserTasks)
	if errors.Is(err, dsl.ErrPlanTooLarge) {
		return nil, &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{
			Code:     domain.BPMNErrTaskLimitExceeded,
			NodeID:   rootID,
			Message:  fmt.Sprintf("process exceeds the %d task limit once its calls are expanded, counting each call and every stage it brings in", validator.MaxUserTasks),
			Severity: domain.SeverityError,
		}}}
	}
	if err != nil {
		return nil, fmt.Errorf("bpmn compile: expand calls: %w", err)
	}
	return expanded, nil
}

func authoredNode(d dsl.DepartmentDef, s dsl.StageDef) string {
	if call, _, inModule := strings.Cut(d.ID, dsl.CallScopeSeparator); inModule {
		return call
	}
	return s.NodeID
}

func unassignedTasks(plan *dsl.CompiledPlan) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for _, d := range plan.Departments {
		for _, s := range d.Stages {
			if !s.CreatesHumanTask() || len(s.DefaultAssignees) > 0 {
				continue
			}
			errs = append(errs, domain.BPMNValidationError{
				Code:   domain.BPMNErrCandidateUsersEmpty,
				NodeID: authoredNode(d, s),
				Message: fmt.Sprintf("task %s/%s names no default user; name one in its candidateUsers or in the Assignees of a call that runs it, "+
					"since a published workflow gives every task a default", d.ID, s.NodeID),
				Severity: domain.SeverityError,
			})
		}
	}
	return errs
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
	root, rootErr := runnableRoot(defs)
	if rootErr != nil {
		return nil, nil, nil, &domain.ValidationFailedError{Errors: append(callGraphErrs, *rootErr)}
	}

	errs := slices.Concat(callGraphErrs, c.validateSingleRoot(bpmnXML, root, defs))
	if hasBlockingErrors(errs) {
		return nil, nil, nil, &domain.ValidationFailedError{Errors: errs}
	}

	plan, called, err = bpmncore.Compile(root, bpmncore.BuildGraph(root), c.stageTypes, c.elements, defs)
	if err != nil {
		return nil, nil, nil, compileFailure("bpmn compile", root.ID, err)
	}
	return plan, called, root, nil
}

func runnableRoot(defs *bpmncore.BPMNDefinitions) (*bpmncore.BPMNProcess, *domain.BPMNValidationError) {
	roots := executableProcesses(defs)
	switch len(roots) {
	case 1:
		return roots[0], nil
	case 0:
		return nil, &domain.BPMNValidationError{
			Code:     domain.BPMNErrNoRunnableProcess,
			Message:  "no process runs: every pool is marked ignore, not executable, or called by another; leave the ignore property off the pool the workflow runs",
			Severity: domain.SeverityError,
		}
	}
	return nil, &domain.BPMNValidationError{
		Code:     domain.BPMNErrMultipleProcesses,
		Message:  fmt.Sprintf("%d pools would run; a workflow runs one, so mark every external pool with the zeebe property ignore=true", len(roots)),
		Severity: domain.SeverityError,
	}
}

func executableProcesses(defs *bpmncore.BPMNDefinitions) []*bpmncore.BPMNProcess {
	if len(defs.Processes) == 1 {
		if bpmncore.IsIgnored(&defs.Processes[0]) {
			return nil
		}
		return []*bpmncore.BPMNProcess{&defs.Processes[0]}
	}
	calledIDs := map[string]bool{}
	for i := range defs.Processes {
		if bpmncore.IsIgnored(&defs.Processes[i]) {
			continue
		}
		for _, ca := range defs.Processes[i].CallActivities {
			if ca.ExtensionElements.ZeebeCalledElement != nil && ca.ExtensionElements.ZeebeCalledElement.ProcessID != "" {
				calledIDs[ca.ExtensionElements.ZeebeCalledElement.ProcessID] = true
			}
		}
	}
	var out []*bpmncore.BPMNProcess
	for i := range defs.Processes {
		p := &defs.Processes[i]
		if p.IsExecutable && !calledIDs[p.ID] && !bpmncore.IsIgnored(p) {
			out = append(out, p)
		}
	}
	return out
}

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
	h, err := canonicalBundleHash(defs)
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
