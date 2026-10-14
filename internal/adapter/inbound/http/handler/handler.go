package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type workflowSvc interface {
	List(ctx context.Context, tenantID uuid.UUID, filter port.WorkflowFilter) ([]*domain.Workflow, int64, error)
	Create(ctx context.Context, tenantID, userID uuid.UUID, templateKey, name, description, bpmnXML, planTier string) (*domain.Workflow, *domain.WorkflowVersion, error)
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
	Nodes(ctx context.Context, tenantID, workflowID, versionID uuid.UUID) (*service.VersionNodes, error)
}

type validationSvc interface {
	Validate(ctx context.Context, tenantID uuid.UUID, bpmnXML string) (bool, []domain.BPMNValidationError, error)
	ValidateVersion(ctx context.Context, tenantID uuid.UUID, v *domain.WorkflowVersion) (bool, []domain.BPMNValidationError, error)
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
	Usages(ctx context.Context, callerTenantID, moduleID uuid.UUID) ([]*domain.ModuleUsage, error)
	Contract(ctx context.Context, callerTenantID, moduleID, versionID uuid.UUID) (*service.ModuleContract, error)
}

type starterSvc interface {
	List(ctx context.Context, callerTenantID uuid.UUID, filter port.StarterFilter) ([]*domain.StarterTemplate, int64, error)
	Create(ctx context.Context, callerTenantID, userID uuid.UUID, req service.CreateStarterReq) (*domain.StarterTemplate, error)
	Get(ctx context.Context, callerTenantID, id uuid.UUID) (*domain.StarterTemplate, error)
	CreateFromWorkflowVersion(ctx context.Context, callerTenantID, userID, sourceVersionID uuid.UUID, req service.CreateFromWorkflowVersionReq) (*domain.StarterTemplate, error)
	Delete(ctx context.Context, callerTenantID, id uuid.UUID) error
}

type compiledPlanSvc interface {
	Get(ctx context.Context, tenantID, versionID uuid.UUID) (*service.CompiledPlan, error)
}

type Handler struct {
	workflows      workflowSvc
	drafts         draftSvc
	versions       versionSvc
	validation     validationSvc
	modules        moduleSvc
	starters       starterSvc
	membership     membershipRevoker
	connectors     connectorSvc
	compiledPlans  compiledPlanSvc
	cache          port.CacheStore
	idempotencyTTL time.Duration
	log            port.Logger
}

type Services struct {
	Workflows      workflowSvc
	Drafts         draftSvc
	Versions       versionSvc
	Validation     validationSvc
	Modules        moduleSvc
	Starters       starterSvc
	Membership     membershipRevoker
	Connectors     connectorSvc
	CompiledPlans  compiledPlanSvc
	Cache          port.CacheStore
	IdempotencyTTL time.Duration
	Log            port.Logger
}

func New(s Services) *Handler {
	nameFieldsByJSONTag.Do(registerJSONFieldNames)
	return &Handler{
		workflows:      s.Workflows,
		drafts:         s.Drafts,
		versions:       s.Versions,
		validation:     s.Validation,
		modules:        s.Modules,
		starters:       s.Starters,
		membership:     s.Membership,
		connectors:     s.Connectors,
		compiledPlans:  s.CompiledPlans,
		cache:          s.Cache,
		idempotencyTTL: s.IdempotencyTTL,
		log:            s.Log,
	}
}

var nameFieldsByJSONTag sync.Once

func registerJSONFieldNames() {
	engine, ok := binding.Validator.Engine().(*validator.Validate)
	if !ok {
		return
	}
	engine.RegisterTagNameFunc(func(f reflect.StructField) string {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			return f.Name
		}
		return name
	})
}

var adminRoles = map[string]bool{
	"tenant_admin": true,
	"tenant_owner": true,
}

func isAdmin(c *gin.Context) bool {
	id, ok := httpmiddleware.IdentityFrom(c)
	if !ok {
		return false
	}
	for _, role := range id.Roles {
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
	id, ok := httpmiddleware.IdentityFrom(c)
	if !ok {
		return false
	}
	for _, role := range id.Roles {
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
	id, exists := httpmiddleware.IdentityFrom(c)
	if !exists {
		writeProblem(c, http.StatusInternalServerError, CodeInternal, "missing request context", nil)
		return uuid.Nil, uuid.Nil, false
	}
	return id.TenantID, id.UserID, true
}

func mustAdminCtx(c *gin.Context) (tenantID, userID uuid.UUID, ok bool) {
	tenantID, userID, ok = mustCtx(c)
	if !ok || !requireAdmin(c) {
		return uuid.Nil, uuid.Nil, false
	}
	return tenantID, userID, true
}

func (h *Handler) logForbiddenXML(c *gin.Context, err error) {
	if h.log == nil || !errors.Is(err, domain.ErrForbiddenXML) {
		return
	}
	tenantID := "unknown"
	if id, ok := httpmiddleware.IdentityFrom(c); ok {
		tenantID = id.TenantID.String()
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

const (
	maxPage          = 100000
	defaultLimit     = 20
	maxLimit         = 100
	defaultPage      = 1
	maxVersionsLimit = 100
)

type queryInt struct {
	key         string
	def, lo, hi int
}

func parseQueryInts(c *gin.Context, specs ...queryInt) ([]int, bool) {
	values := make([]int, len(specs))
	var invalid []invalidParam
	for i, sp := range specs {
		raw, present := c.GetQuery(sp.key)
		if !present {
			values[i] = sp.def
			continue
		}
		v, err := strconv.Atoi(raw)
		if err != nil || v < sp.lo || v > sp.hi {
			invalid = append(invalid, invalidParam{Name: sp.key, Reason: fmt.Sprintf("must be an integer between %d and %d", sp.lo, sp.hi)})
			continue
		}
		values[i] = v
	}
	if len(invalid) > 0 {
		writeProblem(c, http.StatusBadRequest, CodeInvalidInput, "query parameters are invalid", invalid)
		return nil, false
	}
	return values, true
}

func paginate(c *gin.Context) (page, limit int, ok bool) {
	v, ok := parseQueryInts(c,
		queryInt{"page", defaultPage, 1, maxPage},
		queryInt{"limit", defaultLimit, 1, maxLimit},
	)
	if !ok {
		return 0, 0, false
	}
	return v[0], v[1], true
}

func parseVersionsLimit(c *gin.Context) (int, bool) {
	v, ok := parseQueryInts(c, queryInt{"versions_limit", defaultLimit, 1, maxVersionsLimit})
	if !ok {
		return 0, false
	}
	return v[0], true
}
