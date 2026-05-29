package service

import (
	"context"
	"errors"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type ValidationDeps struct {
	Compiler port.PlanCompiler
	Log      port.Logger
}

type ValidationService struct {
	compiler port.PlanCompiler
	log      port.Logger
}

func NewValidationService(d ValidationDeps) *ValidationService {
	return &ValidationService{
		compiler: d.Compiler,
		log:      d.Log,
	}
}

// Validate runs stateless BPMN parsing and structural/semantic checks with no DB writes.
// Returns isValid=true and an empty slice when the XML passes all rules.
func (s *ValidationService) Validate(_ context.Context, _ string) (isValid bool, errs []domain.BPMNValidationError, err error) {
	return false, nil, errors.New("not implemented")
}
