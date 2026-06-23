package bpmn_compiler

import (
	"context"
	"errors"
	"fmt"

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
		return []domain.BPMNValidationError{{Code: domain.BPMNErrMissingNamespace, Message: err.Error()}}, nil
	}
	if err != nil {
		return nil, classifyParseError("validate", err)
	}

	if len(defs.Processes) != 1 && defs.Collaboration == nil {
		return []domain.BPMNValidationError{{
			Code:    domain.BPMNErrMultipleProcesses,
			Message: fmt.Sprintf("definitions contains %d processes without a collaboration wrapper; use <bpmn:collaboration> for multi-pool BPMN", len(defs.Processes)),
		}}, nil
	}

	rejected := scanRejected(bpmnXML)
	collabErrs := validator.ValidateCollaboration(defs.Collaboration, defs)

	if defs.Collaboration != nil {
		var allErrs []domain.BPMNValidationError
		allErrs = append(allErrs, rejected...)
		allErrs = append(allErrs, collabErrs...)
		for i := range defs.Processes {
			proc := &defs.Processes[i]
			g := bpmncore.BuildGraph(proc)
			allErrs = append(allErrs, validator.Validate(proc, g, c.stageTypes, c.elements, defs)...)
			allErrs = append(allErrs, validator.ValidateDiagramCompleteness(proc, defs)...)
		}
		return allErrs, nil
	}

	proc := &defs.Processes[0]
	g := bpmncore.BuildGraph(proc)
	allErrs := append(rejected, validator.Validate(proc, g, c.stageTypes, c.elements, defs)...)
	allErrs = append(allErrs, validator.ValidateDiagramCompleteness(proc, defs)...)
	return allErrs, nil
}

func (c *Compiler) Compile(ctx context.Context, bpmnXML string) (*domain.CompiledPlan, error) {
	defs, err := parse(ctx, bpmnXML)
	if errors.Is(err, errMissingNamespace) {
		return nil, &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{
			Code: domain.BPMNErrMissingNamespace, Message: err.Error(),
		}}}
	}
	if err != nil {
		return nil, classifyParseError("compile", err)
	}
	if len(defs.Processes) != 1 {
		return nil, &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{
			Code:    domain.BPMNErrMultipleProcesses,
			Message: fmt.Sprintf("Compile expects a single-process BPMN document; got %d processes — use CompileCollaboration for multi-pool BPMN", len(defs.Processes)),
		}}}
	}
	proc := &defs.Processes[0]
	g := bpmncore.BuildGraph(proc)

	errs := append(scanRejected(bpmnXML), validator.Validate(proc, g, c.stageTypes, c.elements, defs)...)
	errs = append(errs, validator.ValidateDiagramCompleteness(proc, defs)...)
	if len(errs) > 0 {
		return nil, &domain.ValidationFailedError{Errors: errs}
	}

	plan, err := bpmncore.Compile(proc, g, c.stageTypes, c.elements, defs)
	if err != nil {
		return nil, fmt.Errorf("bpmn compile: %w", err)
	}
	return plan, nil
}

func (c *Compiler) CompileCollaboration(ctx context.Context, bpmnXML string) (*domain.CompiledCollaboration, error) {
	defs, err := parse(ctx, bpmnXML)
	if errors.Is(err, errMissingNamespace) {
		return nil, &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{
			Code: domain.BPMNErrMissingNamespace, Message: err.Error(),
		}}}
	}
	if err != nil {
		return nil, classifyParseError("compile-collaboration", err)
	}
	if defs.Collaboration == nil {
		return nil, &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{
			Code:    domain.BPMNErrMultipleProcesses,
			Message: "CompileCollaboration requires a <bpmn:collaboration> element; use Compile for single-process BPMN",
		}}}
	}

	var valErrs []domain.BPMNValidationError
	valErrs = append(valErrs, scanRejected(bpmnXML)...)
	valErrs = append(valErrs, validator.ValidateCollaboration(defs.Collaboration, defs)...)
	for i := range defs.Processes {
		proc := &defs.Processes[i]
		g := bpmncore.BuildGraph(proc)
		valErrs = append(valErrs, validator.Validate(proc, g, c.stageTypes, c.elements, defs)...)
	}
	if len(valErrs) > 0 {
		return nil, &domain.ValidationFailedError{Errors: valErrs}
	}

	procByID := make(map[string]*bpmncore.BPMNProcess, len(defs.Processes))
	for i := range defs.Processes {
		procByID[defs.Processes[i].ID] = &defs.Processes[i]
	}

	var plans []*domain.CompiledPlan
	for _, participant := range defs.Collaboration.Participants {
		proc, ok := procByID[participant.ProcessRef]
		if !ok {
			continue
		}
		g := bpmncore.BuildGraph(proc)
		plan, err := bpmncore.Compile(proc, g, c.stageTypes, c.elements, defs)
		if err != nil {
			return nil, fmt.Errorf("bpmn compile-collaboration participant %q: %w", participant.ID, err)
		}
		bpmncore.QualifyPlanDepts(plan, plan.Name)
		plans = append(plans, plan)
	}

	msgNameByID := make(map[string]string, len(defs.Messages))
	for _, m := range defs.Messages {
		msgNameByID[m.ID] = m.Name
	}
	elementToProcess := buildElementToProcessName(defs.Processes)

	var messages []domain.MessageDef
	for _, mf := range defs.Collaboration.MessageFlows {
		name := mf.Name
		if name == "" && mf.MessageRef != "" {
			name = msgNameByID[mf.MessageRef]
		}
		messages = append(messages, domain.MessageDef{
			Name:       name,
			SourcePlan: elementToProcess[mf.SourceRef],
			TargetPlan: elementToProcess[mf.TargetRef],
		})
	}

	return &domain.CompiledCollaboration{Plans: plans, Messages: messages}, nil
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
	}
	return m
}

func (c *Compiler) Hash(bpmnXML string) (string, error) {
	defs, err := parse(context.Background(), bpmnXML)
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
