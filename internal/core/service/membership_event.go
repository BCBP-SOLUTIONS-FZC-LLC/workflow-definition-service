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
	assignees, err := s.assignees.ListByUser(ctx, tenantID, userID)
	if err != nil {
		return fmt.Errorf("list assignees: %w", err)
	}

	nodesByVersion := make(map[uuid.UUID][]string)
	for _, a := range assignees {
		if a.DepartmentID != departmentID {
			continue
		}
		nodesByVersion[a.WorkflowVersionID] = append(nodesByVersion[a.WorkflowVersionID], a.NodeKey)
	}

	for versionID, nodeKeys := range nodesByVersion {
		if err := s.invalidateVersion(ctx, tenantID, versionID, nodeKeys); err != nil {
			return err
		}
	}

	if s.execution != nil {
		if err := s.execution.PauseUserTasks(ctx, tenantID, userID); err != nil {
			return fmt.Errorf("pause user tasks: %w", err)
		}
	}

	// Record as processed after all side effects complete. Moving this after the
	// work ensures a transient PauseUserTasks failure causes the consumer to retry
	// rather than silently skip on the next delivery (at-least-once guarantee).
	// Both invalidateVersion and PauseUserTasks are idempotent, so re-running on
	// concurrent delivery is safe.
	isNew, err := s.processedEvents.RecordIfNew(ctx, eventID, "membership-wf-q", "DepartmentMembershipRevoked")
	if err != nil {
		return fmt.Errorf("record processed event: %w", err)
	}
	if !isNew {
		return nil
	}

	s.log.Info("membership revoked handled", map[string]any{
		"event_id":          eventID.String(),
		"tenant_id":         tenantID.String(),
		"user_id":           userID.String(),
		"department_id":     departmentID,
		"versions_affected": len(nodesByVersion),
	})
	return nil
}

func (s *VersionService) invalidateVersion(
	ctx context.Context,
	tenantID, versionID uuid.UUID,
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

	if err := s.versions.SetInvalid(ctx, tenantID, versionID, errorsJSON); err != nil {
		return fmt.Errorf("set invalid: %w", err)
	}
	s.invalidatePlanCache(ctx, tenantID, versionID)
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
