package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
	occurredAt time.Time,
) error {
	assignees, err := s.assignees.ListByUser(ctx, tenantID, userID)
	if err != nil {
		return fmt.Errorf("list assignees: %w", err)
	}

	deptUUID, err := uuid.Parse(departmentID)
	if err != nil {
		return fmt.Errorf("invalid department id %q: %w", departmentID, err)
	}

	return s.invalidateAssigneeVersions(ctx, eventID, tenantID, userID, occurredAt, assignees, &deptUUID, departmentID)
}

func (s *VersionService) HandleTenantMembershipRevoked(
	ctx context.Context,
	eventID, tenantID, userID uuid.UUID,
	occurredAt time.Time,
) error {
	assignees, err := s.assignees.ListByUser(ctx, tenantID, userID)
	if err != nil {
		return fmt.Errorf("list assignees: %w", err)
	}
	return s.invalidateAssigneeVersions(ctx, eventID, tenantID, userID, occurredAt, assignees, nil, "")
}

func (s *VersionService) invalidateAssigneeVersions(
	ctx context.Context,
	eventID, tenantID, userID uuid.UUID,
	occurredAt time.Time,
	assignees []*domain.NodeAssignee,
	departmentID *uuid.UUID,
	departmentLogValue string,
) error {
	nodesByVersion := make(map[uuid.UUID][]string)
	for _, a := range assignees {
		if departmentID != nil && a.DepartmentID != *departmentID {
			continue
		}
		if !occurredAt.IsZero() && a.CreatedAt.After(occurredAt) {
			continue
		}
		nodesByVersion[a.WorkflowVersionID] = append(nodesByVersion[a.WorkflowVersionID], a.NodeKey)
	}

	var evictionErrs []error
	for versionID, nodeKeys := range nodesByVersion {
		if err := s.invalidateVersion(ctx, tenantID, versionID, nodeKeys); err != nil {
			var evictErr *evictionError
			if !errors.As(err, &evictErr) {
				return err
			}
			evictionErrs = append(evictionErrs, err)
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
	return errors.Join(evictionErrs...)
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
	return evictCompiledPlan(ctx, s.cache, tenantID, versionID)
}

func (s *VersionService) skipDeletedVersion(versionID uuid.UUID, err error) error {
	if !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("get version %s: %w", versionID, err)
	}
	s.log.Warn("membership revoked: version no longer exists", map[string]any{"version_id": versionID.String()})
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
