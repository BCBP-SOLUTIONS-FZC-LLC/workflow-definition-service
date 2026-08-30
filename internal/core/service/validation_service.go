package service

import (
	"context"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
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
	moduleXMLs []string,
) (isValid bool, errs []domain.BPMNValidationError, err error) {
	bundled, err := s.compiler.Bundle(bpmnXML, moduleXMLs)
	if err != nil {
		return false, nil, fmt.Errorf("bundle modules: %w", err)
	}
	validationErrs, err := s.compiler.Validate(ctx, bundled)
	if err != nil {
		return false, nil, fmt.Errorf("validate bpmn: %w", err)
	}
	if hasBlockingError(validationErrs) {
		observability.IncCounter(observability.WFValidationFailuresTotal)
		return false, validationErrs, nil
	}
	return true, validationErrs, nil
}

func hasBlockingError(errs []domain.BPMNValidationError) bool {
	for _, e := range errs {
		if e.Severity == domain.SeverityError {
			return true
		}
	}
	return false
}
