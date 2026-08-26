package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type createModuleReq struct {
	Name        string `json:"name"        binding:"required"`
	Description string `json:"description"`
	BPMNXML     string `json:"bpmn_xml"    binding:"required"`
	Scope       string `json:"scope"`
}

type addModuleVersionReq struct {
	BPMNXML *string `json:"bpmn_xml"`
}

type moduleResp struct {
	ID              uuid.UUID  `json:"id"`
	TenantID        *uuid.UUID `json:"tenant_id,omitempty"`
	Scope           string     `json:"scope"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	ActiveVersionID *uuid.UUID `json:"active_version_id,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type moduleVersionResp struct {
	ID            uuid.UUID            `json:"id"`
	ModuleID      uuid.UUID            `json:"module_id"`
	Scope         string               `json:"scope"`
	Status        domain.VersionStatus `json:"status"`
	BPMNXML       string               `json:"bpmn_xml"`
	ProcessID     string               `json:"process_id"`
	VersionNumber *int32               `json:"version_number,omitempty"`
	IsValid       bool                 `json:"is_valid"`
	PublishedAt   *time.Time           `json:"published_at,omitempty"`
	CreatedAt     time.Time            `json:"created_at"`
}

type getModuleResp struct {
	moduleResp
	ActiveVersion *moduleVersionResp `json:"active_version,omitempty"`
}

func toModuleResp(m *domain.Module) moduleResp {
	return moduleResp{
		ID:              m.ID,
		TenantID:        m.TenantID,
		Scope:           string(m.Scope),
		Name:            m.Name,
		Description:     m.Description,
		ActiveVersionID: m.ActiveVersionID,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
}

func toModuleVersionResp(v *domain.ModuleVersion) moduleVersionResp {
	return moduleVersionResp{
		ID:            v.ID,
		ModuleID:      v.ModuleID,
		Scope:         string(v.Scope),
		Status:        v.Status,
		BPMNXML:       v.BPMNXML,
		ProcessID:     v.ProcessID,
		VersionNumber: v.VersionNumber,
		IsValid:       v.IsValid,
		PublishedAt:   v.PublishedAt,
		CreatedAt:     v.CreatedAt,
	}
}

func parseModuleScope(c *gin.Context, raw string) (domain.CatalogScope, bool) {
	switch raw {
	case "", string(domain.ScopeTenant):
		return domain.ScopeTenant, true
	case string(domain.ScopeGlobal):
		return domain.ScopeGlobal, true
	default:
		writeProblem(c, http.StatusUnprocessableEntity, CodeInvalidInput,
			"scope must be \"tenant\" or \"global\"", nil)
		return "", false
	}
}

// parseScopeFilter validates an optional "scope" list-query parameter,
// distinct from parseModuleScope's create-time default-to-tenant behavior:
// here an empty value means "no filter" (both scopes), not "tenant". Rejecting
// anything else up front keeps a typo'd value from reaching the catalog_scope
// Postgres enum comparison, which would otherwise surface as a raw 500.
func parseScopeFilter(c *gin.Context, raw string) (*domain.CatalogScope, bool) {
	switch raw {
	case "":
		return nil, true
	case string(domain.ScopeTenant), string(domain.ScopeGlobal):
		scope := domain.CatalogScope(raw)
		return &scope, true
	default:
		writeProblem(c, http.StatusUnprocessableEntity, CodeInvalidInput,
			"scope must be \"tenant\" or \"global\"", nil)
		return nil, false
	}
}

func (h *Handler) ListModules(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}

	page, limit := paginate(c)
	filter := port.ModuleFilter{Page: page, Limit: limit}
	scope, ok := parseScopeFilter(c, c.Query("scope"))
	if !ok {
		return
	}
	filter.Scope = scope
	if q := c.Query("q"); q != "" {
		filter.Search = &q
	}

	modules, total, err := h.modules.List(c.Request.Context(), tenantID, filter)
	if err != nil {
		errResponse(c, h.log, err)
		return
	}

	resp := make([]moduleResp, len(modules))
	for i, m := range modules {
		resp[i] = toModuleResp(m)
	}
	c.JSON(http.StatusOK, gin.H{
		"modules": resp,
		"pagination": gin.H{
			"total_count": total,
			"page":        page,
			"limit":       limit,
		},
	})
}

func (h *Handler) CreateModule(c *gin.Context) {
	tenantID, userID, ok := mustAdminCtx(c)
	if !ok {
		return
	}

	var req createModuleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindErrResponse(c, err)
		return
	}
	scope, ok := parseModuleScope(c, req.Scope)
	if !ok {
		return
	}
	if scope == domain.ScopeGlobal && !requirePlatformOperator(c) {
		return
	}

	m, v, err := h.modules.Create(c.Request.Context(), tenantID, userID, service.CreateModuleReq{
		Name:        req.Name,
		Description: req.Description,
		BPMNXML:     req.BPMNXML,
		Scope:       scope,
	})
	if err != nil {
		h.logForbiddenXML(c, err)
		errResponse(c, h.log, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"module_id":  m.ID,
		"version_id": v.ID,
		"status":     v.Status,
		"message":    "Module created",
	})
}

func (h *Handler) GetModule(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}
	moduleID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	m, active, err := h.modules.Get(c.Request.Context(), tenantID, moduleID)
	if err != nil {
		errResponse(c, h.log, err)
		return
	}

	resp := getModuleResp{moduleResp: toModuleResp(m)}
	if active != nil {
		v := toModuleVersionResp(active)
		resp.ActiveVersion = &v
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) ListModuleVersions(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}
	moduleID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	page, limit := paginate(c)

	versions, total, err := h.modules.ListVersions(c.Request.Context(), tenantID, moduleID, page, limit)
	if err != nil {
		errResponse(c, h.log, err)
		return
	}

	resp := make([]moduleVersionResp, len(versions))
	for i, v := range versions {
		resp[i] = toModuleVersionResp(v)
	}
	c.JSON(http.StatusOK, gin.H{
		"versions": resp,
		"pagination": gin.H{
			"total_count": total,
			"page":        page,
			"limit":       limit,
		},
	})
}

func (h *Handler) GetModuleVersion(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}
	moduleID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}

	v, err := h.modules.GetVersion(c.Request.Context(), tenantID, moduleID, versionID)
	if err != nil {
		errResponse(c, h.log, err)
		return
	}
	c.JSON(http.StatusOK, toModuleVersionResp(v))
}

func (h *Handler) AddModuleVersion(c *gin.Context) {
	tenantID, userID, ok := mustAdminCtx(c)
	if !ok {
		return
	}
	moduleID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	m, _, err := h.modules.Get(c.Request.Context(), tenantID, moduleID)
	if err != nil {
		errResponse(c, h.log, err)
		return
	}
	if m.Scope == domain.ScopeGlobal && !requirePlatformOperator(c) {
		return
	}

	var req addModuleVersionReq
	_ = c.ShouldBindJSON(&req) // body is optional; nil bpmn_xml copies forward the active version

	v, err := h.modules.AddVersion(c.Request.Context(), tenantID, userID, moduleID, service.AddModuleVersionReq{
		BPMNXML: req.BPMNXML,
	})
	if err != nil {
		h.logForbiddenXML(c, err)
		errResponse(c, h.log, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"module_id":  moduleID,
		"version_id": v.ID,
		"status":     v.Status,
		"message":    "Module version added",
	})
}

func (h *Handler) PublishModuleVersion(c *gin.Context) {
	tenantID, userID, ok := mustAdminCtx(c)
	if !ok {
		return
	}
	moduleID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}

	m, _, err := h.modules.Get(c.Request.Context(), tenantID, moduleID)
	if err != nil {
		errResponse(c, h.log, err)
		return
	}
	if m.Scope == domain.ScopeGlobal && !requirePlatformOperator(c) {
		return
	}

	v, err := h.modules.Publish(c.Request.Context(), tenantID, userID, moduleID, versionID)
	if err != nil {
		errResponse(c, h.log, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"module_id":      moduleID,
		"version_id":     v.ID,
		"status":         v.Status,
		"version_number": v.VersionNumber,
		"message":        "Module version published",
	})
}

func (h *Handler) ArchiveModule(c *gin.Context) {
	tenantID, userID, ok := mustAdminCtx(c)
	if !ok {
		return
	}
	moduleID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	m, _, err := h.modules.Get(c.Request.Context(), tenantID, moduleID)
	if err != nil {
		errResponse(c, h.log, err)
		return
	}
	if m.Scope == domain.ScopeGlobal && !requirePlatformOperator(c) {
		return
	}

	if err := h.modules.Archive(c.Request.Context(), tenantID, userID, moduleID); err != nil {
		errResponse(c, h.log, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"module_id": moduleID,
		"status":    "ARCHIVED",
		"message":   "Module archived successfully",
	})
}
