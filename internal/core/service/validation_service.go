package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

type ValidationDeps struct {
	Compiler       port.PlanCompiler
	ModuleVersions port.ModuleVersionRepository
	Log            port.Logger
}

type ValidationService struct {
	modules moduleResolver
	log     port.Logger
}

func NewValidationService(d ValidationDeps) *ValidationService {
	return &ValidationService{
		modules: moduleResolver{compiler: d.Compiler, versions: d.ModuleVersions},
		log:     logOrNoop(d.Log),
	}
}

func (s *ValidationService) Validate(
	ctx context.Context,
	tenantID uuid.UUID,
	bpmnXML string,
) (isValid bool, errs []domain.BPMNValidationError, err error) {
	return s.validate(ctx, s.modules.live(tenantID), bpmnXML)
}

func (s *ValidationService) ValidateVersion(
	ctx context.Context,
	tenantID uuid.UUID,
	v *domain.WorkflowVersion,
) (isValid bool, errs []domain.BPMNValidationError, err error) {
	lookup := s.modules.live(tenantID)
	if v.Status != domain.VersionStatusDraft {
		lookup = s.modules.snapshot(tenantID, v.ModuleRefs)
	}
	return s.validate(ctx, lookup, v.BPMNXML)
}

func (s *ValidationService) validate(ctx context.Context, lookup moduleLookup, bpmnXML string) (bool, []domain.BPMNValidationError, error) {
	errs, err := s.modules.validateForPublish(ctx, lookup, bpmnXML)
	if err != nil {
		return false, nil, err
	}
	if hasBlockingError(errs) {
		observability.IncCounter(observability.WFValidationFailuresTotal)
		return false, errs, nil
	}
	return true, errs, nil
}

func hasBlockingError(errs []domain.BPMNValidationError) bool {
	for _, e := range errs {
		if e.Severity != domain.SeverityWarning {
			return true
		}
	}
	return false
}
