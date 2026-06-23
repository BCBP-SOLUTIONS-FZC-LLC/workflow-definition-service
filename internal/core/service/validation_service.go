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

// Validate performs no DB writes — safe to call without a transaction.
func (s *ValidationService) Validate(
	ctx context.Context,
	bpmnXML string,
) (isValid bool, errs []domain.BPMNValidationError, err error) {
	validationErrs, err := s.compiler.Validate(ctx, bpmnXML)
	if err != nil {
		return false, nil, fmt.Errorf("validate bpmn: %w", err)
	}
	if len(validationErrs) > 0 {
		wfValidationFailuresTotal.Inc()
		return false, validationErrs, nil
	}
	return true, nil, nil
}
