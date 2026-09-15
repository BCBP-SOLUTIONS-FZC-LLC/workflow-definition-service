package bpmn_compiler

import (
	"context"
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
	if defs.Collaboration != nil {
		return c.validateCollaboration(rejected, defs), nil
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
