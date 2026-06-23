package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type WorkflowDeps struct {
	Transactor port.Transactor
	Workflows  port.WorkflowRepository
	Versions   port.WorkflowVersionRepository
	Cache      port.CacheStore
	Execution  port.ExecutionService
	Compiler   port.PlanCompiler
	Log        port.Logger
}

type WorkflowService struct {
	transactor port.Transactor
	workflows  port.WorkflowRepository
	versions   port.WorkflowVersionRepository
	cache      port.CacheStore
	execution  port.ExecutionService
	compiler   port.PlanCompiler
	log        port.Logger
}

func NewWorkflowService(d WorkflowDeps) *WorkflowService {
	return &WorkflowService{
		transactor: d.Transactor,
		workflows:  d.Workflows,
		versions:   d.Versions,
		cache:      d.Cache,
		execution:  d.Execution,
		compiler:   d.Compiler,
		log:        logOrNoop(d.Log),
	}
}

func (s *WorkflowService) List(
	ctx context.Context,
	tenantID uuid.UUID,
	filter port.WorkflowFilter,
) ([]*domain.Workflow, int64, error) {
	workflows, count, err := s.workflows.List(ctx, tenantID, filter)
	if err != nil {
		return nil, 0, fmt.Errorf("list workflows: %w", err)
	}
	return workflows, count, nil
}

func (s *WorkflowService) Create(
	ctx context.Context,
	tenantID, userID uuid.UUID,
	businessKey, name, description, bpmnXML, planTier string,
) (wf *domain.Workflow, v *domain.WorkflowVersion, err error) {
	defer func() { wfSubmissionsTotal.WithLabelValues(outcomeLabel(err)).Inc() }()
	if s.compiler != nil {
		errs, err := s.compiler.Validate(ctx, bpmnXML)
		if err != nil {
			return nil, nil, err
		}
		if len(errs) > 0 {
			return nil, nil, &domain.ValidationFailedError{Errors: errs}
		}
	}

	wfID, err := uuid.NewV7()
	if err != nil {
		return nil, nil, fmt.Errorf("generate workflow id: %w", err)
	}
	vID, err := uuid.NewV7()
	if err != nil {
		return nil, nil, fmt.Errorf("generate version id: %w", err)
	}
	wf = &domain.Workflow{
		ID:              wfID,
		TenantID:        tenantID,
		CreatedByUserID: userID,
		BusinessKey:     businessKey,
		Name:            name,
		Description:     description,
	}
	v = &domain.WorkflowVersion{
		ID:              vID,
		WorkflowID:      wf.ID,
		TenantID:        tenantID,
		Status:          domain.VersionStatusDraft,
		BPMNXML:         bpmnXML,
		CreatedByUserID: userID,
		IsValid:         true,
	}
	// The plan-quota COUNT and the inserts run in one SERIALIZABLE transaction so
	// two concurrent creates cannot both pass the quota check (LLD §5.6.2).
	if err := s.transactor.RunInTxWithRetry(ctx, func(ctx context.Context) error {
		if err := s.enforceWorkflowQuota(ctx, tenantID, planTier); err != nil {
			return err
		}
		if err := s.workflows.Create(ctx, wf); err != nil {
			return err
		}
		return s.versions.Create(ctx, v)
	}); err != nil {
		return nil, nil, fmt.Errorf("create workflow: %w", err)
	}
	s.log.Info("workflow created", map[string]any{
		"tenant_id":   tenantID.String(),
		"user_id":     userID.String(),
		"workflow_id": wf.ID.String(),
		"version_id":  v.ID.String(),
		"key":         businessKey,
	})
	return wf, v, nil
}

func (s *WorkflowService) Get(
	ctx context.Context,
	tenantID, id uuid.UUID,
	versionsLimit int,
) (*domain.Workflow, []*domain.WorkflowVersion, error) {
	wf, err := s.workflows.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, nil, fmt.Errorf("get workflow: %w", err)
	}
	if versionsLimit < 1 || versionsLimit > 100 {
		versionsLimit = 20
	}
	versions, _, err := s.versions.ListByWorkflow(ctx, tenantID, id, 1, versionsLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("list versions: %w", err)
	}
	return wf, versions, nil
}

func (s *WorkflowService) enforceWorkflowQuota(ctx context.Context, tenantID uuid.UUID, planTier string) error {
	limit := workflowQuotaLimit(planTier)
	if limit <= 0 {
		return nil
	}
	count, err := s.workflows.CountByTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("count workflows: %w", err)
	}
	if count >= limit {
		return domain.ErrPlanQuotaExceeded
	}
	return nil
}

func (s *WorkflowService) Archive(ctx context.Context, tenantID, userID, id uuid.UUID) (err error) {
	defer func() { wfArchiveTotal.WithLabelValues(outcomeLabel(err)).Inc() }()
	wf, err := s.workflows.GetByID(ctx, tenantID, id)
	if err != nil {
		return fmt.Errorf("get workflow: %w", err)
	}
	if wf.ActiveVersionID == nil {
		return domain.ErrNoActiveVersion
	}

	if s.execution != nil {
		hasActive, _, err := s.execution.CheckActiveInstances(ctx, tenantID, id)
		if err != nil {
			return fmt.Errorf("check active instances: %w", err)
		}
		if hasActive {
			return domain.ErrActiveInstancesExist
		}
	}

	versionID := *wf.ActiveVersionID
	if err := s.transactor.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.versions.Archive(ctx, tenantID, versionID); err != nil {
			return err
		}
		return s.workflows.UpdateActiveVersion(ctx, tenantID, id, nil)
	}); err != nil {
		return fmt.Errorf("archive workflow: %w", err)
	}
	if s.cache != nil {
		if err := s.cache.Del(ctx, domain.CompiledPlanCacheKey(tenantID, versionID)); err != nil {
			s.log.Warn("compiled-plan cache invalidation failed", map[string]any{
				"version_id": versionID.String(), "error": err.Error(),
			})
		}
	}
	s.log.Info("workflow archived", map[string]any{
		"tenant_id":   tenantID.String(),
		"user_id":     userID.String(),
		"workflow_id": id.String(),
		"version_id":  versionID.String(),
	})
	return nil
}
