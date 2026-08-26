package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type StarterDeps struct {
	Starters       port.StarterTemplateRepository
	GlobalStarters port.StarterTemplateRepository
	Versions       port.WorkflowVersionRepository
	Compiler       port.PlanCompiler
	Log            port.Logger
}

type StarterService struct {
	starters       port.StarterTemplateRepository
	globalStarters port.StarterTemplateRepository
	versions       port.WorkflowVersionRepository
	compiler       port.PlanCompiler
	log            port.Logger
}

func NewStarterService(d StarterDeps) *StarterService {
	return &StarterService{
		starters:       d.Starters,
		globalStarters: d.GlobalStarters,
		versions:       d.Versions,
		compiler:       d.Compiler,
		log:            logOrNoop(d.Log),
	}
}

func (s *StarterService) startersFor(scope domain.CatalogScope) port.StarterTemplateRepository {
	if scope == domain.ScopeGlobal {
		return s.globalStarters
	}
	return s.starters
}

func (s *StarterService) List(
	ctx context.Context,
	callerTenantID uuid.UUID,
	filter port.StarterFilter,
) ([]*domain.StarterTemplate, int64, error) {
	starters, total, err := s.starters.List(ctx, callerTenantID, filter)
	if err != nil {
		return nil, 0, fmt.Errorf("list starters: %w", err)
	}
	return starters, total, nil
}

type CreateStarterReq struct {
	Name        string
	Description string
	Category    string
	BPMNXML     string
	Scope       domain.CatalogScope
}

func (s *StarterService) Create(
	ctx context.Context,
	callerTenantID, userID uuid.UUID,
	req CreateStarterReq,
) (*domain.StarterTemplate, error) {
	scope := req.Scope
	if scope == "" {
		scope = domain.ScopeTenant
	}

	errs, err := s.compiler.Validate(ctx, req.BPMNXML)
	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return nil, &domain.ValidationFailedError{Errors: errs}
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate starter id: %w", err)
	}
	var tenantID *uuid.UUID
	if scope != domain.ScopeGlobal {
		tenantID = &callerTenantID
	}
	st := &domain.StarterTemplate{
		ID:              id,
		TenantID:        tenantID,
		Scope:           scope,
		Name:            req.Name,
		Description:     req.Description,
		Category:        req.Category,
		BPMNXML:         req.BPMNXML,
		CreatedByUserID: &userID,
	}
	if err := s.startersFor(scope).Create(ctx, st); err != nil {
		return nil, fmt.Errorf("create starter: %w", err)
	}
	s.log.Info("starter created", map[string]any{
		"tenant_id":  callerTenantID.String(),
		"user_id":    userID.String(),
		"starter_id": st.ID.String(),
		"scope":      string(scope),
	})
	return st, nil
}

func (s *StarterService) Get(
	ctx context.Context,
	callerTenantID, id uuid.UUID,
) (*domain.StarterTemplate, error) {
	st, err := s.starters.GetByID(ctx, callerTenantID, id)
	if err != nil {
		return nil, fmt.Errorf("get starter: %w", err)
	}
	return st, nil
}

type CreateFromWorkflowVersionReq struct {
	Name        string
	Description string
	Category    string
}

func (s *StarterService) CreateFromWorkflowVersion(
	ctx context.Context,
	callerTenantID, userID, sourceVersionID uuid.UUID,
	req CreateFromWorkflowVersionReq,
) (*domain.StarterTemplate, error) {
	source, err := s.versions.GetByID(ctx, callerTenantID, sourceVersionID)
	if err != nil {
		return nil, fmt.Errorf("get source workflow version: %w", err)
	}
	if source.Status == domain.VersionStatusDraft {
		return nil, domain.ErrVersionNotPublished
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate starter id: %w", err)
	}
	st := &domain.StarterTemplate{
		ID:                      id,
		TenantID:                &callerTenantID,
		Scope:                   domain.ScopeTenant,
		Name:                    req.Name,
		Description:             req.Description,
		Category:                req.Category,
		BPMNXML:                 source.BPMNXML,
		SourceWorkflowVersionID: &sourceVersionID,
		CreatedByUserID:         &userID,
	}
	if err := s.starters.Create(ctx, st); err != nil {
		return nil, fmt.Errorf("create starter from workflow version: %w", err)
	}
	s.log.Info("starter created from workflow version", map[string]any{
		"tenant_id":         callerTenantID.String(),
		"user_id":           userID.String(),
		"starter_id":        st.ID.String(),
		"source_version_id": sourceVersionID.String(),
	})
	return st, nil
}

func (s *StarterService) Delete(ctx context.Context, callerTenantID, id uuid.UUID) error {
	st, err := s.starters.GetByID(ctx, callerTenantID, id)
	if err != nil {
		return fmt.Errorf("get starter: %w", err)
	}
	if err := s.startersFor(st.Scope).Delete(ctx, id); err != nil {
		return fmt.Errorf("delete starter: %w", err)
	}
	s.log.Info("starter deleted", map[string]any{
		"tenant_id":  callerTenantID.String(),
		"starter_id": id.String(),
	})
	return nil
}
