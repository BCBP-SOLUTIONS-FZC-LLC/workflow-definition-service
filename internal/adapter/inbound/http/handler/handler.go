package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type workflowSvc interface {
	List(ctx context.Context, tenantID uuid.UUID, filter port.WorkflowFilter) ([]*domain.Workflow, int64, error)
	Create(ctx context.Context, tenantID, userID uuid.UUID, businessKey, name, description, bpmnXML, planTier string) (*domain.Workflow, *domain.WorkflowVersion, error)
	Get(ctx context.Context, tenantID, id uuid.UUID, versionsLimit int) (*domain.Workflow, []*domain.WorkflowVersion, error)
	Archive(ctx context.Context, tenantID, userID, id uuid.UUID) error
}

type draftSvc interface {
	Get(ctx context.Context, tenantID, workflowID uuid.UUID) (*domain.WorkflowVersion, error)
	Init(ctx context.Context, tenantID, userID, workflowID uuid.UUID) (*domain.WorkflowVersion, error)
	Update(ctx context.Context, tenantID, userID, workflowID uuid.UUID, req service.UpdateDraftReq) (*domain.WorkflowVersion, error)
	Discard(ctx context.Context, tenantID, workflowID uuid.UUID) error
}

type versionSvc interface {
	List(ctx context.Context, tenantID, workflowID uuid.UUID, page, limit int) ([]*domain.WorkflowVersion, int64, error)
	Get(ctx context.Context, tenantID, workflowID, versionID uuid.UUID) (*domain.WorkflowVersion, error)
	Publish(ctx context.Context, tenantID, userID, workflowID, versionID uuid.UUID, forcePublishStructural bool) (*domain.WorkflowVersion, error)
	Clone(ctx context.Context, tenantID, userID, workflowID, versionID uuid.UUID, planTier string, req service.CloneReq) (*domain.Workflow, *domain.WorkflowVersion, error)
	Promote(ctx context.Context, tenantID, userID, workflowID, versionID uuid.UUID) (*domain.WorkflowVersion, error)
	Export(ctx context.Context, tenantID, workflowID, versionID uuid.UUID) (string, string, error)
	Diff(ctx context.Context, tenantID, workflowID, baseVersionID, targetVersionID uuid.UUID) (*service.DiffResult, error)
}

type validationSvc interface {
	Validate(ctx context.Context, bpmnXML string, moduleXMLs []string) (bool, []domain.BPMNValidationError, error)
}

type Handler struct {
	workflows  workflowSvc
	drafts     draftSvc
	versions   versionSvc
	validation validationSvc
	membership membershipRevoker
	log        port.Logger
}

type Services struct {
	Workflows  workflowSvc
	Drafts     draftSvc
	Versions   versionSvc
	Validation validationSvc
	Membership membershipRevoker
	Log        port.Logger
}

func New(s Services) *Handler {
	return &Handler{
		workflows:  s.Workflows,
		drafts:     s.Drafts,
		versions:   s.Versions,
		validation: s.Validation,
		membership: s.Membership,
		log:        s.Log,
	}
}

func mustCtx(c *gin.Context) (tenantID, userID uuid.UUID, ok bool) {
	rc, exists := gincommon.RequestContext(c)
	if !exists {
		writeProblem(c, http.StatusInternalServerError, CodeInternal, "missing request context", nil)
		return uuid.Nil, uuid.Nil, false
	}
	tenantID, err := uuid.Parse(rc.TenantID)
	if err != nil {
		writeProblem(c, http.StatusInternalServerError, CodeInternal, "invalid tenant id in context", nil)
		return uuid.Nil, uuid.Nil, false
	}
	userID, err = uuid.Parse(rc.UserID)
	if err != nil {
		writeProblem(c, http.StatusInternalServerError, CodeInternal, "invalid user id in context", nil)
		return uuid.Nil, uuid.Nil, false
	}
	return tenantID, userID, true
}

// logForbiddenXML emits an internal security-alert log when a BPMN parse failure
// tripped a forbidden-construct guard (DOCTYPE/entity or the XML-bomb token cap).
// The client response is unchanged (400 INVALID_BPMN_XML, set by errResponse) —
// detection is never disclosed to the caller; this is telemetry only.
func (h *Handler) logForbiddenXML(c *gin.Context, err error) {
	if h.log == nil || !errors.Is(err, domain.ErrForbiddenXML) {
		return
	}
	tenantID := "unknown"
	if rc, ok := gincommon.RequestContext(c); ok {
		tenantID = rc.TenantID
	}
	h.log.Warn("forbidden XML construct rejected", map[string]any{
		"client_ip": c.ClientIP(),
		"tenant_id": tenantID,
		"error":     err.Error(),
	})
}

func parseUUIDParam(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		writeProblem(c, http.StatusBadRequest, CodeBadRequest,
			fmt.Sprintf("invalid %s: not a valid UUID", param), nil)
		return uuid.Nil, false
	}
	return id, true
}

func paginate(c *gin.Context) (page, limit int) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ = strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	return
}

// boundedQuery reads an integer query param, returning def when it is absent,
// unparseable, or outside [min, max]. Mirrors paginate's lenient-clamp policy so
// a hostile or fat-fingered value can never reach the service / SQL LIMIT.
func boundedQuery(c *gin.Context, key string, def, min, max int) int {
	v, err := strconv.Atoi(c.DefaultQuery(key, strconv.Itoa(def)))
	if err != nil || v < min || v > max {
		return def
	}
	return v
}
