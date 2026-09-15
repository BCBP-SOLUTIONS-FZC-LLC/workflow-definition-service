package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"
	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

type membershipRevoker interface {
	HandleMembershipRevoked(ctx context.Context, eventID, tenantID, userID uuid.UUID, departmentID string) error
	HandleTenantMembershipRevoked(ctx context.Context, eventID, tenantID, userID uuid.UUID) error
}

type membershipRevokedPayload struct {
	UserID string `json:"user_id"`
	// DepartmentID is parsed as a UUID by HandleMembershipRevoked today, but
	// its format is owned by the Org & Membership Service and not formally
	// confirmed on our side — see definition_service.md §7.4.2.
	DepartmentID string `json:"department_id"`
	Role         string `json:"role"`
}

// Consumer retry contract: 2xx = handled (incl. dedup no-op), 400 = malformed (non-retryable), 500 = transient (retry).
func (h *Handler) HandleInternalEvent(c *gin.Context) {
	var env events.Envelope[json.RawMessage]
	if err := c.ShouldBindJSON(&env); err != nil {
		observability.IncCounterVec(observability.InternalEventsIngestTotal, "unknown", "bad_payload")
		writeProblem(c, http.StatusBadRequest, CodeBadRequest, "invalid event envelope", nil)
		return
	}

	switch env.Type {
	case "DepartmentMembershipRevoked":
		h.handleMembershipRevoked(c, env)
	case "MembershipRevoked":
		h.handleTenantMembershipRevoked(c, env)
	default:
		h.acknowledgeUnknownEventType(c, env.Type)
	}
}

func (h *Handler) acknowledgeUnknownEventType(c *gin.Context, eventType string) {
	if h.log != nil {
		h.log.Info("internal events: ignoring unhandled type", map[string]any{"event_type": eventType})
	}
	observability.IncCounterVec(observability.InternalEventsIngestTotal, eventType, "ok")
	c.Status(http.StatusOK)
}

// revocationIdentity is the (event, tenant, user) triple both revocation
// events carry. ok=false means a 4xx has already been written.
type revocationIdentity struct {
	eventID, tenantID, userID uuid.UUID
	departmentID              string
	ctx                       context.Context
}

func (h *Handler) parseRevocation(c *gin.Context, env events.Envelope[json.RawMessage], evtType string) (revocationIdentity, bool) {
	badPayload := func(msg string) (revocationIdentity, bool) {
		observability.IncCounterVec(observability.InternalEventsIngestTotal, evtType, "bad_payload")
		writeProblem(c, http.StatusBadRequest, CodeBadRequest, msg, nil)
		return revocationIdentity{}, false
	}

	var p membershipRevokedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return badPayload("invalid " + evtType + " payload")
	}
	eventID, err := uuid.Parse(env.ID)
	if err != nil {
		return badPayload("invalid event id")
	}
	tenantID, err := uuid.Parse(env.TenantID)
	if err != nil {
		return badPayload("invalid tenant_id")
	}
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return badPayload("invalid user_id in payload")
	}

	return revocationIdentity{
		eventID: eventID, tenantID: tenantID, userID: userID, departmentID: p.DepartmentID,
		// Service-to-service call: no gateway identity headers, so inject the RLS GUC
		// from the envelope tenant_id directly (mirrors the gRPC path, §14).
		ctx: pgcommon.WithGUCSet(c.Request.Context(), pgdomain.GUCSet{TenantID: env.TenantID}),
	}, true
}

func (h *Handler) finishRevocation(c *gin.Context, evtType, logMsg string, err error) {
	if err != nil {
		observability.IncCounterVec(observability.InternalEventsIngestTotal, evtType, "error")
		if h.log != nil {
			h.log.Error(logMsg, map[string]any{"error": err.Error()})
		}
		writeProblem(c, http.StatusInternalServerError, CodeInternal, "failed to process event", nil)
		return
	}
	observability.IncCounterVec(observability.InternalEventsIngestTotal, evtType, "ok")
	c.Status(http.StatusOK)
}

func (h *Handler) handleMembershipRevoked(c *gin.Context, env events.Envelope[json.RawMessage]) {
	const evtType = "DepartmentMembershipRevoked"
	in, ok := h.parseRevocation(c, env, evtType)
	if !ok {
		return
	}
	err := h.membership.HandleMembershipRevoked(in.ctx, in.eventID, in.tenantID, in.userID, in.departmentID)
	h.finishRevocation(c, evtType, "internal events: HandleMembershipRevoked failed", err)
}

// handleTenantMembershipRevoked handles IAM's tenant-level MembershipRevoked.
// It carries no department_id — the user left the tenant outright — so it
// deliberately does not reuse the department-scoped handler above.
func (h *Handler) handleTenantMembershipRevoked(c *gin.Context, env events.Envelope[json.RawMessage]) {
	const evtType = "MembershipRevoked"
	in, ok := h.parseRevocation(c, env, evtType)
	if !ok {
		return
	}
	err := h.membership.HandleTenantMembershipRevoked(in.ctx, in.eventID, in.tenantID, in.userID)
	h.finishRevocation(c, evtType, "internal events: HandleTenantMembershipRevoked failed", err)
}
