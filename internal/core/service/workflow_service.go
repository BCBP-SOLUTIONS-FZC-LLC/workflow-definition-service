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
	Outbox     port.OutboxRepository
	Cache      port.CacheStore
	Execution  port.ExecutionService
	Compiler   port.PlanCompiler
	Log        port.Logger
}

type WorkflowService struct {
	transactor port.Transactor
	workflows  port.WorkflowRepository
	versions   port.WorkflowVersionRepository
	outbox     port.OutboxRepository
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
		outbox:     d.Outbox,
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
	businessKey, name, description, bpmnXML string,
) (*domain.Workflow, *domain.WorkflowVersion, error) {
	if s.compiler != nil {
		errs, err := s.compiler.Validate(ctx, bpmnXML)
		if err != nil {
			return nil, nil, err
		}
		if len(errs) > 0 {
			return nil, nil, &domain.ValidationFailedError{Errors: errs}
		}
	}

	wf := &domain.Workflow{
		ID:              uuid.New(),
		TenantID:        tenantID,
		CreatedByUserID: userID,
		BusinessKey:     businessKey,
		Name:            name,
		Description:     description,
	}
	v := &domain.WorkflowVersion{
		ID:              uuid.New(),
		WorkflowID:      wf.ID,
		TenantID:        tenantID,
		Status:          domain.VersionStatusDraft,
		BPMNXML:         bpmnXML,
		CreatedByUserID: userID,
		IsValid:         true,
	}
	if err := s.transactor.RunInTx(ctx, func(ctx context.Context) error {
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

func (s *WorkflowService) Archive(ctx context.Context, tenantID, userID, id uuid.UUID) error {
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
	env, err := buildEnvelope(domain.EventTypeTemplateArchived, tenantID.String(), domain.TemplateArchivedPayload{
		WorkflowID: id.String(),
		VersionID:  versionID.String(),
		ArchivedBy: userID.String(),
	})
	if err != nil {
		return fmt.Errorf("build event: %w", err)
	}

	if err := s.transactor.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.versions.Archive(ctx, tenantID, versionID); err != nil {
			return err
		}
		if err := s.workflows.UpdateActiveVersion(ctx, tenantID, id, nil); err != nil {
			return err
		}
		return s.outbox.Enqueue(ctx, env)
	}); err != nil {
		return err
	}
	s.log.Info("workflow archived", map[string]any{
		"tenant_id":   tenantID.String(),
		"user_id":     userID.String(),
		"workflow_id": id.String(),
		"version_id":  versionID.String(),
	})
	return nil
}
