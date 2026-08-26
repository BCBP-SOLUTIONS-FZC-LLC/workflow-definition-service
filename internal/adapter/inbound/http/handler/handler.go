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

type moduleSvc interface {
	List(ctx context.Context, callerTenantID uuid.UUID, filter port.ModuleFilter) ([]*domain.Module, int64, error)
	Create(ctx context.Context, callerTenantID, userID uuid.UUID, req service.CreateModuleReq) (*domain.Module, *domain.ModuleVersion, error)
	Get(ctx context.Context, callerTenantID, moduleID uuid.UUID) (*domain.Module, *domain.ModuleVersion, error)
	ListVersions(ctx context.Context, callerTenantID, moduleID uuid.UUID, page, limit int) ([]*domain.ModuleVersion, int64, error)
	GetVersion(ctx context.Context, callerTenantID, moduleID, versionID uuid.UUID) (*domain.ModuleVersion, error)
	AddVersion(ctx context.Context, callerTenantID, userID, moduleID uuid.UUID, req service.AddModuleVersionReq) (*domain.ModuleVersion, error)
	Publish(ctx context.Context, callerTenantID, userID, moduleID, versionID uuid.UUID) (*domain.ModuleVersion, error)
	Archive(ctx context.Context, callerTenantID, userID, moduleID uuid.UUID) error
}

type Handler struct {
	workflows  workflowSvc
	drafts     draftSvc
	versions   versionSvc
	validation validationSvc
	modules    moduleSvc
	membership membershipRevoker
	connectors connectorSvc
	log        port.Logger
}

type Services struct {
	Workflows  workflowSvc
	Drafts     draftSvc
	Versions   versionSvc
	Validation validationSvc
	Modules    moduleSvc
	Membership membershipRevoker
	Connectors connectorSvc
	Log        port.Logger
}

func New(s Services) *Handler {
	return &Handler{
		workflows:  s.Workflows,
		drafts:     s.Drafts,
		versions:   s.Versions,
		validation: s.Validation,
		modules:    s.Modules,
		membership: s.Membership,
		connectors: s.Connectors,
		log:        s.Log,
	}
}

var adminRoles = map[string]bool{
	"tenant_admin": true,
	"tenant_owner": true,
}

func isAdmin(c *gin.Context) bool {
	rc, ok := gincommon.RequestContext(c)
	if !ok {
		return false
	}
	for _, role := range rc.Roles {
		if adminRoles[role] {
			return true
		}
	}
	return false
}

func requireAdmin(c *gin.Context) bool {
	if !isAdmin(c) {
		writeProblem(c, http.StatusForbidden, CodeForbidden, "caller lacks tenant_admin/tenant_owner role", nil)
		return false
	}
	return true
}

const platformOperatorRole = "platform_operator"

func isPlatformOperator(c *gin.Context) bool {
	rc, ok := gincommon.RequestContext(c)
	if !ok {
		return false
	}
	for _, role := range rc.Roles {
		if role == platformOperatorRole {
			return true
		}
	}
	return false
}

func requirePlatformOperator(c *gin.Context) bool {
	if !isPlatformOperator(c) {
		writeProblem(c, http.StatusForbidden, CodeForbidden, "caller lacks platform_operator role", nil)
		return false
	}
	return true
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

// mustAdminCtx is mustCtx plus the Admin-only gate (§3.2) combined, for the
// handlers that need both checks in sequence.
func mustAdminCtx(c *gin.Context) (tenantID, userID uuid.UUID, ok bool) {
	tenantID, userID, ok = mustCtx(c)
	if !ok || !requireAdmin(c) {
		return uuid.Nil, uuid.Nil, false
	}
	return tenantID, userID, true
}

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

func boundedQuery(c *gin.Context, key string, def, min, max int) int {
	v, err := strconv.Atoi(c.DefaultQuery(key, strconv.Itoa(def)))
	if err != nil || v < min || v > max {
		return def
	}
	return v
}
