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
	Transactor port.Transactor
	Cache      port.CacheStore
	Workflows  port.WorkflowRepository
	Versions   port.WorkflowVersionRepository
	Assignees  port.AssigneeRepository
	Compiler   port.PlanCompiler
	Log        port.Logger
}

type DraftService struct {
	transactor port.Transactor
	cache      port.CacheStore
	workflows  port.WorkflowRepository
	versions   port.WorkflowVersionRepository
	assignees  port.AssigneeRepository
	compiler   port.PlanCompiler
	log        port.Logger
}

func NewDraftService(d DraftDeps) *DraftService {
	return &DraftService{
		transactor: d.Transactor,
		cache:      d.Cache,
		workflows:  d.Workflows,
		versions:   d.Versions,
		assignees:  d.Assignees,
		compiler:   d.Compiler,
		log:        logOrNoop(d.Log),
	}
}

type UpdateDraftReq struct {
	Name           *string
	Description    *string
	BPMNXML        *string
	ModuleBPMNXMLs *[]string // nil = no change; &[]string{} = clear all modules
	RecordVersion  int64     // optimistic-lock token (0 = unchecked)
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

	draftID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate draft id: %w", err)
	}
	draft := &domain.WorkflowVersion{
		ID:              draftID,
		WorkflowID:      workflowID,
		TenantID:        tenantID,
		Status:          domain.VersionStatusDraft,
		BPMNXML:         active.BPMNXML,
		ModuleBPMNXMLs:  active.ModuleBPMNXMLs,
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
	releaseLock, err := s.acquireDraftLock(ctx, tenantID, workflowID)
	if err != nil {
		return nil, err
	}
	defer releaseLock()

	draft, err := s.versions.GetDraft(ctx, tenantID, workflowID)
	if err != nil {
		return nil, fmt.Errorf(errGetDraft, err)
	}

	if err := failFastOnStaleRecordVersion(draft, req.RecordVersion); err != nil {
		return nil, err
	}

	if req.BPMNXML != nil {
		draft.BPMNXML = *req.BPMNXML
		clearCompiledArtifacts(draft)
		draft.IsValid = true
	}

	if req.ModuleBPMNXMLs != nil {
		draft.ModuleBPMNXMLs = *req.ModuleBPMNXMLs
		clearCompiledArtifacts(draft)
	}

	if err := s.runUpdateTx(ctx, tenantID, workflowID, draft, req); err != nil {
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

func (s *DraftService) runUpdateTx(
	ctx context.Context,
	tenantID, workflowID uuid.UUID,
	draft *domain.WorkflowVersion,
	req UpdateDraftReq,
) error {
	updatesWorkflowMetadata := req.Name != nil || req.Description != nil

	doUpdate := func(ctx context.Context) error {
		if updatesWorkflowMetadata {
			if err := s.updateWorkflowMeta(ctx, tenantID, workflowID, req); err != nil {
				return err
			}
		}
		if err := s.versions.UpdateDraft(ctx, draft); err != nil {
			return fmt.Errorf("update draft: %w", err)
		}
		return nil
	}

	if updatesWorkflowMetadata && s.transactor != nil {
		return s.transactor.RunInTx(ctx, doUpdate)
	}
	return doUpdate(ctx)
}

// acquireDraftLock returns a no-op release when Cache isn't configured
// (dev/test), so draft locking is best-effort rather than a hard dependency.
func (s *DraftService) acquireDraftLock(ctx context.Context, tenantID, workflowID uuid.UUID) (release func(), err error) {
	if s.cache == nil {
		return func() {}, nil
	}
	lockKey := fmt.Sprintf("draft-lock:%s:%s", tenantID, workflowID)
	acquired, err := s.cache.SetNX(ctx, lockKey, "1", 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("acquire draft lock: %w", err)
	}
	if !acquired {
		return nil, domain.ErrDraftConcurrency
	}
	return func() { _ = s.cache.Del(ctx, lockKey) }, nil
}

// failFastOnStaleRecordVersion rejects an obvious mismatch before any write.
// The authoritative guard is still the SQL record_version predicate in the
// repo, which also catches a write that slips in between this check and the
// update — this is a fast-fail complement to it, not a replacement.
func failFastOnStaleRecordVersion(draft *domain.WorkflowVersion, recordVersion int64) error {
	if recordVersion == 0 {
		return nil
	}
	if draft.RecordVersion != recordVersion {
		return domain.ErrDraftConcurrency
	}
	draft.RecordVersion = recordVersion
	return nil
}

func clearCompiledArtifacts(draft *domain.WorkflowVersion) {
	draft.CompiledPlanJSON = nil
	draft.ArtifactHash = ""
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
