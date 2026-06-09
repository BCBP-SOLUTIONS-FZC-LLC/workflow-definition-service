package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type DraftDeps struct {
	Cache     port.CacheStore
	Workflows port.WorkflowRepository
	Versions  port.WorkflowVersionRepository
	Assignees port.AssigneeRepository
	Compiler  port.PlanCompiler
	Log       port.Logger
}

type DraftService struct {
	cache     port.CacheStore
	workflows port.WorkflowRepository
	versions  port.WorkflowVersionRepository
	assignees port.AssigneeRepository
	compiler  port.PlanCompiler
	log       port.Logger
}

func NewDraftService(d DraftDeps) *DraftService {
	return &DraftService{
		cache:     d.Cache,
		workflows: d.Workflows,
		versions:  d.Versions,
		assignees: d.Assignees,
		compiler:  d.Compiler,
		log:       logOrNoop(d.Log),
	}
}

// UpdateDraftReq carries optional fields that may be patched on the draft.
type UpdateDraftReq struct {
	Name          *string
	Description   *string
	BPMNXML       *string
	LastUpdatedAt *time.Time // for optimistic concurrency check
}

const errGetDraft = "get draft: %w"

func (s *DraftService) Get(ctx context.Context, tenantID, workflowID uuid.UUID) (*domain.WorkflowVersion, error) {
	v, err := s.versions.GetDraft(ctx, tenantID, workflowID)
	if err != nil {
		return nil, fmt.Errorf(errGetDraft, err)
	}
	return v, nil
}

func (s *DraftService) Init(
	ctx context.Context,
	tenantID, userID, workflowID uuid.UUID,
) (*domain.WorkflowVersion, error) {
	wf, err := s.workflows.GetByID(ctx, tenantID, workflowID)
	if err != nil {
		return nil, fmt.Errorf("get workflow: %w", err)
	}
	if wf.ActiveVersionID == nil {
		return nil, domain.ErrNoActiveVersion
	}

	active, err := s.versions.GetByID(ctx, tenantID, *wf.ActiveVersionID)
	if err != nil {
		return nil, fmt.Errorf("get active version: %w", err)
	}

	draft := &domain.WorkflowVersion{
		ID:              uuid.New(),
		WorkflowID:      workflowID,
		TenantID:        tenantID,
		Status:          domain.VersionStatusDraft,
		BPMNXML:         active.BPMNXML,
		CreatedByUserID: userID,
		IsValid:         true,
	}
	if err := s.versions.Create(ctx, draft); err != nil {
		return nil, fmt.Errorf("create draft: %w", err)
	}
	s.log.Info("draft initialized", map[string]any{
		"tenant_id":   tenantID.String(),
		"user_id":     userID.String(),
		"workflow_id": workflowID.String(),
		"version_id":  draft.ID.String(),
	})
	return draft, nil
}

func (s *DraftService) Update(
	ctx context.Context,
	tenantID, userID, workflowID uuid.UUID,
	req UpdateDraftReq,
) (*domain.WorkflowVersion, error) {
	// Fail-open when Cache is not configured (dev/test environments).
	if s.cache != nil {
		lockKey := fmt.Sprintf("draft-lock:%s", workflowID)
		acquired, err := s.cache.SetNX(ctx, lockKey, "1", 30*time.Second)
		if err != nil {
			return nil, fmt.Errorf("acquire draft lock: %w", err)
		}
		if !acquired {
			return nil, domain.ErrDraftConcurrency
		}
		defer s.cache.Del(ctx, lockKey) //nolint:errcheck
	}

	draft, err := s.versions.GetDraft(ctx, tenantID, workflowID)
	if err != nil {
		return nil, fmt.Errorf(errGetDraft, err)
	}

	if req.LastUpdatedAt != nil && !draft.UpdatedAt.Equal(*req.LastUpdatedAt) {
		return nil, domain.ErrDraftConcurrency
	}

	if req.Name != nil || req.Description != nil {
		if err := s.updateWorkflowMeta(ctx, tenantID, workflowID, req); err != nil {
			return nil, err
		}
	}

	if req.BPMNXML != nil {
		draft.BPMNXML = *req.BPMNXML
		// Clear stale compilation artefacts so the next publish recompiles.
		draft.CompiledPlanJSON = nil
		draft.ArtifactHash = ""
		draft.IsValid = true
	}
	draft.UpdatedAt = time.Now()

	if err := s.versions.UpdateDraft(ctx, draft); err != nil {
		return nil, fmt.Errorf("update draft: %w", err)
	}
	s.log.Info("draft updated", map[string]any{
		"tenant_id":   tenantID.String(),
		"user_id":     userID.String(),
		"workflow_id": workflowID.String(),
		"version_id":  draft.ID.String(),
	})
	return draft, nil
}

func (s *DraftService) updateWorkflowMeta(
	ctx context.Context,
	tenantID, workflowID uuid.UUID,
	req UpdateDraftReq,
) error {
	wf, err := s.workflows.GetByID(ctx, tenantID, workflowID)
	if err != nil {
		return fmt.Errorf("get workflow: %w", err)
	}
	name := wf.Name
	description := wf.Description
	if req.Name != nil {
		name = *req.Name
	}
	if req.Description != nil {
		description = *req.Description
	}
	if err := s.workflows.UpdateMetadata(ctx, tenantID, workflowID, name, description); err != nil {
		return fmt.Errorf("update workflow metadata: %w", err)
	}
	return nil
}

func (s *DraftService) Discard(ctx context.Context, tenantID, workflowID uuid.UUID) error {
	draft, err := s.versions.GetDraft(ctx, tenantID, workflowID)
	if err != nil {
		return fmt.Errorf(errGetDraft, err)
	}
	if err := s.versions.DeleteDraft(ctx, tenantID, draft.ID); err != nil {
		return fmt.Errorf("delete draft: %w", err)
	}
	s.log.Info("draft discarded", map[string]any{
		"tenant_id":   tenantID.String(),
		"workflow_id": workflowID.String(),
		"version_id":  draft.ID.String(),
	})
	return nil
}
