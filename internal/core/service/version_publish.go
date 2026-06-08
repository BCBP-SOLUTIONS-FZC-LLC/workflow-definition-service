package service

import (
	"context"
	"encoding/json"
	"fmt"

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

// publishPreFlight compiles the BPMN, checks eligibility, and returns everything
// needed to execute the publish inside a transaction.
func (s *VersionService) publishPreFlight(
	ctx context.Context,
	tenantID, workflowID, versionID uuid.UUID,
	skipEligibilityCheck bool,
) (*domain.WorkflowVersion, string, string, int32, []*domain.NodeAssignee, error) {
	draft, err := s.versions.GetByID(ctx, tenantID, versionID)
	if err != nil {
		return nil, "", "", 0, nil, fmt.Errorf("get draft version: %w", err)
	}
	if draft.Status != domain.VersionStatusDraft {
		return nil, "", "", 0, nil, domain.ErrVersionNotDraft
	}
	if draft.WorkflowID != workflowID {
		return nil, "", "", 0, nil, domain.ErrNotFound
	}

	plan, err := s.compiler.Compile(ctx, draft.BPMNXML)
	if err != nil {
		return nil, "", "", 0, nil, fmt.Errorf("compile bpmn: %w", err)
	}
	artifactHash, err := s.compiler.Hash(draft.BPMNXML)
	if err != nil {
		return nil, "", "", 0, nil, fmt.Errorf("hash bpmn: %w", err)
	}

	if !skipEligibilityCheck && s.membership != nil {
		if err := s.checkAssigneeEligibility(ctx, tenantID, plan); err != nil {
			return nil, "", "", 0, nil, err
		}
	}

	versionNumber, err := s.versions.NextVersionNumber(ctx, tenantID, workflowID)
	if err != nil {
		return nil, "", "", 0, nil, fmt.Errorf("next version number: %w", err)
	}
	compiledJSON, err := marshalPlan(plan)
	if err != nil {
		return nil, "", "", 0, nil, fmt.Errorf("marshal compiled plan: %w", err)
	}

	return draft, compiledJSON, artifactHash, versionNumber, extractAssignees(tenantID, versionID, plan), nil
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
			if err := s.checkStageEligibility(ctx, tenantID, dept.ID, stage); err != nil {
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
		if err != nil {
			return fmt.Errorf("%w: invalid assignee UUID %s", domain.ErrAssigneeIneligible, assigneeID)
		}
		eligible, err := s.membership.CheckEligibility(ctx, tenantID, userID, deptID, stage.Role)
		if err != nil {
			return fmt.Errorf("check eligibility: %w", err)
		}
		if !eligible {
			return fmt.Errorf("%w: user %s dept %s role %s", domain.ErrAssigneeIneligible, assigneeID, deptID, stage.Role)
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
			for _, assigneeID := range stage.DefaultAssignees {
				userID, err := uuid.Parse(assigneeID)
				if err != nil {
					continue
				}
				assignees = append(assignees, &domain.NodeAssignee{
					ID:                uuid.New(),
					TenantID:          tenantID,
					WorkflowVersionID: versionID,
					NodeKey:           nodeKey,
					UserID:            userID,
					DepartmentID:      dept.ID,
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
