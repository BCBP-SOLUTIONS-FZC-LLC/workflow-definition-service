package sqs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type membershipRevokedPayload struct {
	UserID       string `json:"user_id"`
	DepartmentID string `json:"department_id"`
	Role         string `json:"role"`
}

// membershipRevoker is the subset of VersionService consumed by this handler.
type membershipRevoker interface {
	HandleMembershipRevoked(ctx context.Context, eventID, tenantID, userID uuid.UUID, departmentID string) error
}

// Handler dispatches inbound SQS events to the appropriate service method.
type Handler struct {
	log     port.Logger
	version membershipRevoker
}

// NewHandler returns the func signature expected by the platform-events SQS consumer.
func NewHandler(log port.Logger, versionSvc membershipRevoker) func(context.Context, events.Envelope[json.RawMessage]) error {
	h := &Handler{log: log, version: versionSvc}
	return h.dispatch
}

func (h *Handler) dispatch(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	switch env.Type {
	case "DepartmentMembershipRevoked":
		return h.handleMembershipRevoked(ctx, env)
	default:
		h.log.Info("sqs: unhandled event type", map[string]any{"event_type": env.Type})
		return nil
	}
}

func (h *Handler) handleMembershipRevoked(ctx context.Context, env events.Envelope[json.RawMessage]) error {
	var p membershipRevokedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		h.log.Error("sqs: failed to unmarshal DepartmentMembershipRevoked payload", map[string]any{"error": err.Error()})
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	eventID, err := uuid.Parse(env.ID)
	if err != nil {
		h.log.Error("sqs: invalid event id", map[string]any{"event_id": env.ID, "error": err.Error()})
		return fmt.Errorf("parse event id: %w", err)
	}
	tenantID, err := uuid.Parse(env.TenantID)
	if err != nil {
		h.log.Error("sqs: invalid tenant_id", map[string]any{"tenant_id": env.TenantID, "error": err.Error()})
		return fmt.Errorf("parse tenant_id: %w", err)
	}
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		h.log.Error("sqs: invalid user_id in payload", map[string]any{"user_id": p.UserID, "error": err.Error()})
		return fmt.Errorf("parse user_id: %w", err)
	}

	return h.version.HandleMembershipRevoked(ctx, eventID, tenantID, userID, p.DepartmentID)
}
