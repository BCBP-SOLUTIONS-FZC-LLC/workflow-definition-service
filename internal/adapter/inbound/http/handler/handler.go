package handler

import (
	"context"
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
	Validate(ctx context.Context, bpmnXML string) (bool, []domain.BPMNValidationError, error)
}

type Handler struct {
	workflows  workflowSvc
	drafts     draftSvc
	versions   versionSvc
	validation validationSvc
}

type Services struct {
	Workflows  workflowSvc
	Drafts     draftSvc
	Versions   versionSvc
	Validation validationSvc
}

func New(s Services) *Handler {
	return &Handler{
		workflows:  s.Workflows,
		drafts:     s.Drafts,
		versions:   s.Versions,
		validation: s.Validation,
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

func parseUUIDParam(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		writeProblem(c, http.StatusBadRequest, CodeNotFound,
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
