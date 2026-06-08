package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type invalidationError struct {
	NodeID string `json:"node_id"`
	Error  string `json:"error"`
}

const invalidationMsg = "Default assignee is no longer eligible: Department membership revoked"

func (s *VersionService) HandleMembershipRevoked(
	ctx context.Context,
	eventID, tenantID, userID uuid.UUID,
	departmentID string,
) error {
	isNew, err := s.processedEvents.RecordIfNew(ctx, eventID, tenantID, "iam-membership")
	if err != nil {
		return fmt.Errorf("record processed event: %w", err)
	}
	if !isNew {
		return nil
	}

	assignees, err := s.assignees.ListByUser(ctx, tenantID, userID)
	if err != nil {
		return fmt.Errorf("list assignees: %w", err)
	}

	// Group node keys by version ID, filtered to the revoked department.
	nodesByVersion := make(map[uuid.UUID][]string)
	for _, a := range assignees {
		if a.DepartmentID != departmentID {
			continue
		}
		nodesByVersion[a.WorkflowVersionID] = append(nodesByVersion[a.WorkflowVersionID], a.NodeKey)
	}

	for versionID, nodeKeys := range nodesByVersion {
		if err := s.invalidateVersion(ctx, tenantID, userID, versionID, nodeKeys); err != nil {
			return err
		}
	}
	return nil
}

func (s *VersionService) invalidateVersion(
	ctx context.Context,
	tenantID, userID, versionID uuid.UUID,
	nodeKeys []string,
) error {
	v, err := s.versions.GetByID(ctx, tenantID, versionID)
	if err != nil {
		// Version may have been deleted; skip rather than fail the whole handler.
		s.log.Error("membership revoked: get version", map[string]any{
			"version_id": versionID.String(),
			"error":      err.Error(),
		})
		return nil
	}
	if v.Status == domain.VersionStatusArchived {
		return nil
	}

	errorsJSON, err := marshalInvalidationErrors(nodeKeys)
	if err != nil {
		return fmt.Errorf("marshal invalidation errors: %w", err)
	}

	if v.Status == domain.VersionStatusPublished {
		var versionNumber int32
		if v.VersionNumber != nil {
			versionNumber = *v.VersionNumber
		}
		return s.transactor.RunInTx(ctx, func(ctx context.Context) error {
			if err := s.versions.SetInvalid(ctx, tenantID, versionID, errorsJSON); err != nil {
				return fmt.Errorf("set invalid: %w", err)
			}
			env, err := buildEnvelope(
				domain.EventTypeTemplateEligibilityInvalidated,
				tenantID.String(),
				domain.TemplateEligibilityInvalidatedPayload{
					WorkflowID:    v.WorkflowID.String(),
					VersionID:     versionID.String(),
					VersionNumber: versionNumber,
					RevokedUserID: userID.String(),
					AffectedNodes: nodeKeys,
					Reason:        "DepartmentMembershipRevoked",
				},
			)
			if err != nil {
				return fmt.Errorf("build eligibility invalidated event: %w", err)
			}
			return s.outbox.Enqueue(ctx, env)
		})
	}

	// DRAFT — invalidate without an outbox event.
	if err := s.versions.SetInvalid(ctx, tenantID, versionID, errorsJSON); err != nil {
		return fmt.Errorf("set invalid: %w", err)
	}
	return nil
}

func marshalInvalidationErrors(nodeKeys []string) (string, error) {
	errs := make([]invalidationError, len(nodeKeys))
	for i, key := range nodeKeys {
		errs[i] = invalidationError{NodeID: key, Error: invalidationMsg}
	}
	b, err := json.Marshal(errs)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	return string(b), nil
}
