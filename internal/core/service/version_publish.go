package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type publishTxInput struct {
	tenantID      uuid.UUID
	workflowID    uuid.UUID
	versionID     uuid.UUID
	versionNumber int32
	compiledJSON  string
	artifactHash  string
	assignees     []*domain.NodeAssignee
	env           events.Envelope[json.RawMessage]
}

func (s *VersionService) publishPreFlight(
	ctx context.Context,
	tenantID, workflowID, versionID uuid.UUID,
	forcePublishStructural bool,
) (*domain.WorkflowVersion, string, string, []*domain.NodeAssignee, string, error) {
	draft, err := s.versions.GetByID(ctx, tenantID, versionID)
	if err != nil {
		return nil, "", "", nil, "", fmt.Errorf("get draft version: %w", err)
	}
	if draft.Status != domain.VersionStatusDraft {
		return nil, "", "", nil, "", domain.ErrVersionNotDraft
	}
	if draft.WorkflowID != workflowID {
		return nil, "", "", nil, "", domain.ErrNotFound
	}

	bundled, err := s.compiler.Bundle(draft.BPMNXML, draft.ModuleBPMNXMLs)
	if err != nil {
		return nil, "", "", nil, "", fmt.Errorf("bundle modules: %w", err)
	}
	compileStart := time.Now()
	plan, err := s.compiler.Compile(ctx, bundled)
	wfCompileDuration.Observe(time.Since(compileStart).Seconds())
	if err != nil {
		return nil, "", "", nil, "", fmt.Errorf("compile bpmn: %w", err)
	}
	artifactHash, err := s.compiler.Hash(ctx, bundled)
	if err != nil {
		return nil, "", "", nil, "", fmt.Errorf("hash bpmn: %w", err)
	}

	var businessKey string
	if s.workflows != nil {
		wf, err := s.workflows.GetByID(ctx, tenantID, workflowID)
		if err != nil {
			return nil, "", "", nil, "", fmt.Errorf(errGetWorkflow, err)
		}
		if err := s.checkStructuralDivergence(ctx, tenantID, wf, plan, forcePublishStructural); err != nil {
			return nil, "", "", nil, "", err
		}
		businessKey = wf.BusinessKey
	}

	if s.membership != nil {
		if err := s.checkAssigneeEligibility(ctx, tenantID, plan); err != nil {
			return nil, "", "", nil, "", err
		}
	}

	compiledJSON, err := marshalPlan(plan)
	if err != nil {
		return nil, "", "", nil, "", fmt.Errorf("marshal compiled plan: %w", err)
	}

	return draft, compiledJSON, artifactHash, extractAssignees(tenantID, versionID, plan), businessKey, nil
}

func (s *VersionService) checkStructuralDivergence(
	ctx context.Context,
	tenantID uuid.UUID,
	wf *domain.Workflow,
	draftPlan *domain.CompiledPlan,
	forcePublishStructural bool,
) error {
	if wf.ActiveVersionID == nil {
		return nil // first publish. no divergence possible
	}

	active, err := s.versions.GetByID(ctx, tenantID, *wf.ActiveVersionID)
	if err != nil {
		return fmt.Errorf("get active version for divergence check: %w", err)
	}

	if active.CompiledPlanJSON == nil || *active.CompiledPlanJSON == "" {
		return nil // no stored baseline to compare; allow publish
	}
	var activePlan domain.CompiledPlan
	if err := json.Unmarshal([]byte(*active.CompiledPlanJSON), &activePlan); err != nil {
		return nil // unreadable baseline; allow publish rather than blocking
	}

	diff := computeDiff(&activePlan, draftPlan)
	if !diff.MetadataOnly && !forcePublishStructural {
		return domain.ErrStructuralDivergence
	}
	return nil
}

func (s *VersionService) runPublishTx(ctx context.Context, in publishTxInput) error {
	if err := s.versions.Publish(ctx, in.tenantID, in.versionID, in.versionNumber, in.compiledJSON, in.artifactHash); err != nil {
		return err
	}
	if err := s.assignees.DeleteByVersion(ctx, in.tenantID, in.versionID); err != nil {
		return err
	}
	if len(in.assignees) > 0 {
		if err := s.assignees.BulkInsert(ctx, in.tenantID, in.versionID, in.assignees); err != nil {
			return err
		}
	}
	if err := s.workflows.UpdateActiveVersion(ctx, in.tenantID, in.workflowID, &in.versionID); err != nil {
		return err
	}
	return s.outbox.Enqueue(ctx, in.env)
}

func (s *VersionService) checkAssigneeEligibility(
	ctx context.Context,
	tenantID uuid.UUID,
	plan *domain.CompiledPlan,
) error {
	for _, dept := range plan.Departments {
		for _, stage := range dept.Stages {
			if err := s.checkStageEligibility(ctx, tenantID, dept.IAMDepartmentID, stage); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *VersionService) checkStageEligibility(
	ctx context.Context,
	tenantID uuid.UUID,
	deptID string,
	stage domain.StageDef,
) error {
	for _, assigneeID := range stage.DefaultAssignees {
		userID, err := uuid.Parse(assigneeID)
		if err != nil || userID.Version() != 7 {
			return fmt.Errorf("invalid assignee UUID %s: %w", assigneeID, domain.ErrAssigneeIneligible)
		}
		eligible, err := s.membership.CheckEligibility(ctx, tenantID, userID, deptID, stage.Role)
		if err != nil {
			return fmt.Errorf("check eligibility: %w", err)
		}
		if !eligible {
			return fmt.Errorf("user %s dept %s role %s: %w", assigneeID, deptID, stage.Role, domain.ErrAssigneeIneligible)
		}
	}
	return nil
}

func extractAssignees(
	tenantID, versionID uuid.UUID,
	plan *domain.CompiledPlan,
) []*domain.NodeAssignee {
	var assignees []*domain.NodeAssignee
	for _, dept := range plan.Departments {
		for _, stage := range dept.Stages {
			nodeKey := dept.ID + "/" + stage.Type
			deptUUID, err := uuid.Parse(dept.IAMDepartmentID)
			if err != nil {
				continue
			}
			for _, assigneeID := range stage.DefaultAssignees {
				userID, err := uuid.Parse(assigneeID)
				if err != nil {
					continue
				}
				assigneeRowID, err := uuid.NewV7()
				if err != nil {
					continue
				}
				assignees = append(assignees, &domain.NodeAssignee{
					ID:                assigneeRowID,
					TenantID:          tenantID,
					WorkflowVersionID: versionID,
					NodeKey:           nodeKey,
					UserID:            userID,
					DepartmentID:      deptUUID,
					Role:              stage.Role,
				})
			}
		}
	}
	return assignees
}

func marshalPlan(plan *domain.CompiledPlan) (string, error) {
	b, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("marshal plan: %w", err)
	}
	return string(b), nil
}
