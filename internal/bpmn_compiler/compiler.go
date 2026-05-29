package bpmn_compiler

import (
	"context"
	"errors"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.PlanCompiler = (*Compiler)(nil)

type Compiler struct{}

func New() *Compiler {
	return &Compiler{}
}

func (c *Compiler) Compile(_ context.Context, _ string) (*domain.CompiledPlan, error) {
	return nil, errors.New("not implemented")
}

func (c *Compiler) Validate(_ context.Context, _ string) ([]domain.BPMNValidationError, error) {
	return nil, errors.New("not implemented")
}

func (c *Compiler) Hash(_ string) (string, error) {
	return "", errors.New("not implemented")
}
