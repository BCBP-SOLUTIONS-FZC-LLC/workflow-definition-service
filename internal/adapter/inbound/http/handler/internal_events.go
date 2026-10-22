package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

const (
	eventDepartmentMembershipRevoked = "DepartmentMembershipRevoked"
	eventMembershipRevoked           = "MembershipRevoked"
	eventTenantMembershipsPurged     = "TenantMembershipsPurged"
	eventTenantStateChanged          = "TenantStateChanged"

	tenantStatusOffboarded = "offboarded"
)

func HandledEventTypes() []string {
	return []string{eventDepartmentMembershipRevoked, eventMembershipRevoked, eventTenantMembershipsPurged, eventTenantStateChanged}
}

type membershipRevoker interface {
	HandleDepartmentMembershipRevoked(ctx context.Context, eventID, tenantID, userID uuid.UUID, departmentID string, occurredAt time.Time) error
	HandleTenantMembershipRevoked(ctx context.Context, eventID, tenantID, userID uuid.UUID, occurredAt time.Time) error
}

type tenantOffboarder interface {
	RecordOffboarded(ctx context.Context, tenantID uuid.UUID, at time.Time) error
}

type membershipRevokedPayload struct {
	UserID       string `json:"user_id"`
	DepartmentID string `json:"department_id"`
	Role         string `json:"role"`
}

func (h *Handler) HandleInternalEvent(c *gin.Context) {
	var env events.Envelope[json.RawMessage]
	if err := c.ShouldBindJSON(&env); err != nil {
		observability.IncCounterVec(observability.InternalEventsIngestTotal, "unknown", "bad_payload")
		if writeIfTooLarge(c, err) {
			return
		}
		writeProblem(c, http.StatusBadRequest, CodeBadRequest, "invalid event envelope", nil)
		return
	}

	if env.SchemaID != "" {
		decoded, ok := h.decodeSchemaRegistryPayload(c, env)
		if !ok {
			return
		}
		env.Payload = decoded
	}

	switch env.Type {
	case eventDepartmentMembershipRevoked:
		h.handleDepartmentMembershipRevoked(c, env)
	case eventMembershipRevoked:
		h.handleTenantMembershipRevoked(c, env)
	case eventTenantMembershipsPurged:
		h.handleTenantOffboarding(c, env, eventTenantMembershipsPurged, true)
	case eventTenantStateChanged:
		h.handleTenantOffboarding(c, env, eventTenantStateChanged, false)
	default:
		h.acknowledgeUnknownEventType(c, env.Type)
	}
}

func (h *Handler) decodeSchemaRegistryPayload(c *gin.Context, env events.Envelope[json.RawMessage]) (json.RawMessage, bool) {
	fail := func(detail string) (json.RawMessage, bool) {
		observability.IncCounterVec(observability.InternalEventsIngestTotal, env.Type, "decode_failed")
		writeProblem(c, http.StatusBadGateway, CodeEventDecodeFailed, detail, nil)
		return nil, false
	}
	var b64 string
	if err := json.Unmarshal(env.Payload, &b64); err != nil {
		return fail("schema-registry payload is not a base64 JSON string")
	}
	framed, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return fail("schema-registry payload base64 decode failed")
	}
	decoded, err := events.GlueDecodeCodec{}.Decode(c.Request.Context(), env.SchemaID, framed)
	if err != nil {
		return fail("schema-registry payload decode failed")
	}
	return decoded, true
}

func (h *Handler) acknowledgeUnknownEventType(c *gin.Context, eventType string) {
	if h.log != nil {
		h.log.Info("internal events: ignoring unhandled type", map[string]any{"event_type": eventType})
	}
	observability.IncCounterVec(observability.InternalEventsIngestTotal, "unknown", "ok")
	c.Status(http.StatusOK)
}

type revocationIdentity struct {
	eventID, tenantID, userID uuid.UUID
	departmentID              string
	occurredAt                time.Time
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
	if evtType == eventDepartmentMembershipRevoked {
		if _, err := uuid.Parse(p.DepartmentID); err != nil {
			return badPayload("invalid department_id in payload")
		}
	}

	return revocationIdentity{
		eventID: eventID, tenantID: tenantID, userID: userID, departmentID: p.DepartmentID, occurredAt: env.Timestamp,
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

func (h *Handler) handleDepartmentMembershipRevoked(c *gin.Context, env events.Envelope[json.RawMessage]) {
	const evtType = eventDepartmentMembershipRevoked
	in, ok := h.parseRevocation(c, env, evtType)
	if !ok {
		return
	}
	err := h.membership.HandleDepartmentMembershipRevoked(in.ctx, in.eventID, in.tenantID, in.userID, in.departmentID, in.occurredAt)
	h.finishRevocation(c, evtType, "internal events: HandleDepartmentMembershipRevoked failed", err)
}

func (h *Handler) handleTenantMembershipRevoked(c *gin.Context, env events.Envelope[json.RawMessage]) {
	const evtType = eventMembershipRevoked
	in, ok := h.parseRevocation(c, env, evtType)
	if !ok {
		return
	}
	err := h.membership.HandleTenantMembershipRevoked(in.ctx, in.eventID, in.tenantID, in.userID, in.occurredAt)
	h.finishRevocation(c, evtType, "internal events: HandleTenantMembershipRevoked failed", err)
}

type tenantStateChangedPayload struct {
	Status string `json:"status"`
}

func (h *Handler) handleTenantOffboarding(c *gin.Context, env events.Envelope[json.RawMessage], evtType string, always bool) {
	if !always {
		var p tenantStateChangedPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			observability.IncCounterVec(observability.InternalEventsIngestTotal, evtType, "bad_payload")
			writeProblem(c, http.StatusBadRequest, CodeBadRequest, "invalid "+evtType+" payload", nil)
			return
		}
		if p.Status != tenantStatusOffboarded {
			observability.IncCounterVec(observability.InternalEventsIngestTotal, evtType, "ok")
			c.Status(http.StatusOK)
			return
		}
	}
	tenantID, err := uuid.Parse(env.TenantID)
	if err != nil {
		observability.IncCounterVec(observability.InternalEventsIngestTotal, evtType, "bad_payload")
		writeProblem(c, http.StatusBadRequest, CodeBadRequest, "invalid tenant_id", nil)
		return
	}
	ctx := pgcommon.WithGUCSet(c.Request.Context(), pgdomain.GUCSet{TenantID: env.TenantID})
	h.finishRevocation(c, evtType, "internal events: recording the offboarded tenant failed", h.offboarding.RecordOffboarded(ctx, tenantID, env.Timestamp))
}
