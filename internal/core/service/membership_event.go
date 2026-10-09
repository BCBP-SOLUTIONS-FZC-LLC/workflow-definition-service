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

func (s *VersionService) HandleDepartmentMembershipRevoked(
	ctx context.Context,
	eventID, tenantID, userID uuid.UUID,
	departmentID string,
) error {
	assignees, err := s.assignees.ListByUser(ctx, tenantID, userID)
	if err != nil {
		return fmt.Errorf("list assignees: %w", err)
	}

	deptUUID, err := uuid.Parse(departmentID)
	if err != nil {
		return fmt.Errorf("invalid department id %q: %w", departmentID, err)
	}

	return s.invalidateAssigneeVersions(ctx, eventID, tenantID, userID, assignees, &deptUUID, departmentID)
}

func (s *VersionService) HandleTenantMembershipRevoked(
	ctx context.Context,
	eventID, tenantID, userID uuid.UUID,
) error {
	assignees, err := s.assignees.ListByUser(ctx, tenantID, userID)
	if err != nil {
		return fmt.Errorf("list assignees: %w", err)
	}
	return s.invalidateAssigneeVersions(ctx, eventID, tenantID, userID, assignees, nil, "")
}

func (s *VersionService) invalidateAssigneeVersions(
	ctx context.Context,
	eventID, tenantID, userID uuid.UUID,
	assignees []*domain.NodeAssignee,
	departmentID *uuid.UUID,
	departmentLogValue string,
) error {
	nodesByVersion := make(map[uuid.UUID][]string)
	for _, a := range assignees {
		if departmentID != nil && a.DepartmentID != *departmentID {
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

	scope := "tenant"
	if departmentID != nil {
		scope = "department"
	}
	s.log.Info("membership revoked handled", map[string]any{
		"event_id":          eventID.String(),
		"tenant_id":         tenantID.String(),
		"user_id":           userID.String(),
		"scope":             scope,
		"department_id":     departmentLogValue,
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
		return s.skipDeletedVersion(versionID, err)
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
	invalidateCompiledPlanCache(ctx, s.cache, s.log, tenantID, versionID)
	return nil
}

func (s *VersionService) skipDeletedVersion(versionID uuid.UUID, err error) error {
	s.log.Error("membership revoked: get version", map[string]any{
		"version_id": versionID.String(),
		"error":      err.Error(),
	})
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
