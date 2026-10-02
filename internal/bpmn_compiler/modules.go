package bpmn_compiler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/element"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const (
	bindingLatest     = "latest"
	bindingVersionTag = "versionTag"
	bindingDeployment = "deployment"
)

func parseVersionTag(tag string) (int32, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(tag), 10, 32)
	if err != nil || n < 1 {
		return 0, false
	}
	return int32(n), true
}

// ModuleCalls lists the calls in bpmnXML that name a library module, in
// document order, and reports calls that cannot (definition LLD §3.3.18). A
// document that does not parse lists nothing; Validate or compile reports it.
func (c *Compiler) ModuleCalls(ctx context.Context, bpmnXML string) ([]domain.ModuleCall, []domain.BPMNValidationError, error) {
	defs, err := parse(ctx, bpmnXML)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, nil, fmt.Errorf("bpmn module calls: %w", err)
	}
	if err != nil {
		return nil, nil, nil
	}
	var calls []domain.ModuleCall
	var issues []domain.BPMNValidationError
	for i := range defs.Processes {
		for _, ca := range defs.Processes[i].CallActivities {
			call, issue, ok := moduleCall(ca, defs.Processes[i].ID, defs)
			if issue != nil {
				issues = append(issues, *issue)
			}
			if ok {
				calls = append(calls, call)
			}
		}
	}
	return calls, issues, nil
}

// moduleCall judges ca, a call made by process callerID. A call pinning a
// version of callerID itself names an earlier version of the same module.
func moduleCall(ca bpmncore.BPMNCallActivity, callerID string, defs *bpmncore.BPMNDefinitions) (domain.ModuleCall, *domain.BPMNValidationError, bool) {
	ext := ca.ExtensionElements.ZeebeCalledElement
	if ext == nil || ext.ProcessID == "" {
		return domain.ModuleCall{}, nil, false // ValidateCallActivity reports the missing calledElement
	}
	name := ca.Name
	if name == "" {
		name = ca.ID
	}
	unresolved := func(format string, args ...any) *domain.BPMNValidationError {
		return &domain.BPMNValidationError{Code: domain.BPMNErrUnresolvedCalledElement, NodeID: ca.ID,
			Message: fmt.Sprintf("call %q ", name) + fmt.Sprintf(format, args...), Severity: domain.SeverityError}
	}
	if strings.HasPrefix(strings.TrimSpace(ext.ProcessID), "=") {
		return domain.ModuleCall{}, unresolved("names its module with an expression; a call must name a module by its process id"), false
	}
	local := bpmncore.FindProcessByID(defs, ext.ProcessID) != nil
	switch ext.BindingType {
	case "", bindingLatest:
		if local {
			return domain.ModuleCall{}, nil, false
		}
		return domain.ModuleCall{CallID: ca.ID, ProcessID: ext.ProcessID}, nil, true
	case bindingDeployment:
		if local {
			return domain.ModuleCall{}, nil, false
		}
		return domain.ModuleCall{}, unresolved("uses bindingType deployment, but process %q is not in this file", ext.ProcessID), false
	case bindingVersionTag:
		n, ok := parseVersionTag(ext.VersionTag)
		if !ok {
			return domain.ModuleCall{}, unresolved("pins versionTag %q; it must be a published module version number", ext.VersionTag), false
		}
		if local && ext.ProcessID != callerID {
			return domain.ModuleCall{}, unresolved("pins version %d of library module %q, but this file also defines a process %q; "+
				"rename the process in this file, or call it without a versionTag", n, ext.ProcessID, ext.ProcessID), false
		}
		return domain.ModuleCall{CallID: ca.ID, ProcessID: ext.ProcessID, VersionNumber: n}, nil, true
	default:
		return domain.ModuleCall{}, unresolved("uses bindingType %q; use latest, versionTag or deployment", ext.BindingType), false
	}
}

// ValidateModule validates a bundled module document with the module's own
// process as the root. Its open lanes are allowed and the diagram rules are
// skipped, since a module runs only inside a workflow; a global module may
// not name a user, because user ids belong to one tenant.
func (c *Compiler) ValidateModule(ctx context.Context, bpmnXML, moduleProcessID string, global bool) ([]domain.BPMNValidationError, error) {
	defs, err := parse(ctx, bpmnXML)
	if errors.Is(err, errMissingNamespace) {
		return []domain.BPMNValidationError{{Code: domain.BPMNErrMissingNamespace, Message: err.Error(), Severity: domain.SeverityError}}, nil
	}
	if err != nil {
		return nil, classifyParseError("validate-module", err)
	}
	root := bpmncore.FindProcessByID(defs, moduleProcessID)
	if root == nil {
		return nil, fmt.Errorf("bpmn validate-module: process %q not in the document", moduleProcessID)
	}
	defs.ModuleProcesses[root.ID] = true
	errs := append(validator.ValidateCallGraph(defs), scanRejected(bpmnXML)...)
	errs = append(errs, validator.Validate(root, bpmncore.BuildGraph(root), c.stageTypes, c.elements, defs)...)
	errs = append(errs, validator.ValidateCalledBodies(root, c.stageTypes, c.elements, defs)...)
	if global {
		errs = append(errs, namedUsers(root)...)
	}
	return errs, nil
}

// CompileModule compiles a bundled module document with the module's own
// process as the root, judged as ValidateModule judges it and shaped as a call
// compiles it, and returns the message names and error codes the bundle
// declares.
func (c *Compiler) CompileModule(ctx context.Context, bpmnXML, moduleProcessID string) (*dsl.CompiledCollaboration, domain.ModuleDeclarations, error) {
	var none domain.ModuleDeclarations
	errs, err := c.ValidateModule(ctx, bpmnXML, moduleProcessID, false)
	if err != nil {
		return nil, none, err
	}
	if blocking := slices.DeleteFunc(errs, func(e domain.BPMNValidationError) bool { return !isBlocking(e) }); len(blocking) > 0 {
		return nil, none, &domain.ValidationFailedError{Errors: blocking}
	}
	defs, err := parse(ctx, bpmnXML)
	if err != nil {
		return nil, none, classifyParseError("compile-module", err)
	}
	root := bpmncore.FindProcessByID(defs, moduleProcessID)
	defs.ModuleProcesses[root.ID] = true
	plan, called, err := bpmncore.Compile(root, bpmncore.BuildGraph(root), c.stageTypes, c.elements, defs)
	if err != nil {
		return nil, none, compileFailure("bpmn compile-module", root.ID, err)
	}
	if plan.Name == "" {
		plan.Name = root.ID
	}
	plan.Departments = element.ShapeCalledPlan(plan.Departments, root)
	collab := &dsl.CompiledCollaboration{
		MainPlan:      plan.Name,
		Plans:         append([]*dsl.CompiledPlan{plan}, called...),
		SchemaVersion: dsl.CurrentSchemaVersion,
	}
	if _, err := expand(collab, root.ID); err != nil {
		return nil, none, err
	}
	return collab, declarations(defs), nil
}

func declarations(defs *bpmncore.BPMNDefinitions) domain.ModuleDeclarations {
	var d domain.ModuleDeclarations
	for _, m := range defs.Messages {
		if m.Name != "" {
			d.Messages = append(d.Messages, m.Name)
		}
	}
	for _, e := range defs.Errors {
		if e.ErrorCode != "" {
			d.ErrorCodes = append(d.ErrorCodes, e.ErrorCode)
		}
	}
	slices.Sort(d.Messages)
	slices.Sort(d.ErrorCodes)
	d.Messages, d.ErrorCodes = slices.Compact(d.Messages), slices.Compact(d.ErrorCodes)
	return d
}

func namedUsers(proc *bpmncore.BPMNProcess) []domain.BPMNValidationError {
	tasks := append([]bpmncore.BPMNUserTask{}, proc.UserTasks...)
	for _, sp := range proc.SubProcesses {
		tasks = append(tasks, sp.UserTasks...)
	}
	var errs []domain.BPMNValidationError
	for _, t := range tasks {
		if ad := t.ExtensionElements.AssignmentDefinition; ad != nil && strings.TrimSpace(ad.CandidateUsers) != "" {
			errs = validator.AppendErr(errs, domain.BPMNErrInvalidCandidateUser, t.ID,
				"a global module cannot name a user, because user ids belong to one tenant; leave candidateUsers empty and let each call or instance assign")
		}
	}
	for _, ca := range proc.CallActivities {
		if in := bpmncore.ParseCallInputs(ca); len(in.Assignees) > 0 {
			errs = validator.AppendErr(errs, domain.BPMNErrInvalidCandidateUser, ca.ID,
				"a global module cannot name a user, because user ids belong to one tenant; remove this call's Assignees and let each workflow or instance assign")
		}
	}
	return errs
}
