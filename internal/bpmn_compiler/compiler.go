package bpmn_compiler

import (
	"context"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.PlanCompiler = (*Compiler)(nil)

type Compiler struct{}

func New() *Compiler {
	return &Compiler{}
}

func (c *Compiler) Validate(ctx context.Context, bpmnXML string) ([]domain.BPMNValidationError, error) {
	defs, err := parse(ctx, bpmnXML)
	if err != nil {
		return nil, fmt.Errorf("bpmn validate: %w", err)
	}
	if len(defs.Processes) != 1 {
		return []domain.BPMNValidationError{{
			Code:    domain.BPMNErrMultipleProcesses,
			Message: fmt.Sprintf("definitions contains %d processes; only one is allowed", len(defs.Processes)),
		}}, nil
	}
	proc := &defs.Processes[0]
	g := buildGraph(proc)
	return validate(proc, g), nil
}

// TaskQueue in the returned plan defaults to the shared queue. The service layer
// overrides it for enterprise-plan tenants before persisting.
func (c *Compiler) Compile(ctx context.Context, bpmnXML string) (*domain.CompiledPlan, error) {
	defs, err := parse(ctx, bpmnXML)
	if err != nil {
		return nil, fmt.Errorf("bpmn compile: %w", err)
	}
	if len(defs.Processes) != 1 {
		return nil, &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{
			Code:    domain.BPMNErrMultipleProcesses,
			Message: fmt.Sprintf("expected exactly one process, got %d", len(defs.Processes)),
		}}}
	}
	proc := &defs.Processes[0]
	g := buildGraph(proc)

	if errs := validate(proc, g); len(errs) > 0 {
		return nil, &domain.ValidationFailedError{Errors: errs}
	}

	plan, err := compile(proc, g)
	if err != nil {
		return nil, fmt.Errorf("bpmn compile: %w", err)
	}
	return plan, nil
}

// Zeebe property ordering within each element does not affect the hash.
// ctx is not part of the port.PlanCompiler.Hash signature; context.Background() is used internally.
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
