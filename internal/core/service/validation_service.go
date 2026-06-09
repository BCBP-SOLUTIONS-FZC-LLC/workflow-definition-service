package service

import (
	"context"
	"fmt"

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
		log:      logOrNoop(d.Log),
	}
}

// Validate runs stateless BPMN parsing and structural/semantic checks with no DB writes.
// Returns isValid=true and an empty slice when the XML passes all rules.
func (s *ValidationService) Validate(
	ctx context.Context,
	bpmnXML string,
) (isValid bool, errs []domain.BPMNValidationError, err error) {
	validationErrs, err := s.compiler.Validate(ctx, bpmnXML)
	if err != nil {
		return false, nil, fmt.Errorf("validate bpmn: %w", err)
	}
	if len(validationErrs) > 0 {
		return false, validationErrs, nil
	}
	return true, nil, nil
}
