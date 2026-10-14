package handler

import (
	"errors"
	"io"
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
	ProcessID       string     `json:"process_id"`
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
		ProcessID:       m.ProcessID,
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

func (h *Handler) requireModuleScopeAccess(c *gin.Context, tenantID, moduleID uuid.UUID) bool {
	m, _, err := h.modules.Get(c.Request.Context(), tenantID, moduleID)
	if err != nil {
		errResponse(c, h.log, err)
		return false
	}
	return m.Scope != domain.ScopeGlobal || requirePlatformOperator(c)
}

func (h *Handler) ListModules(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}

	page, limit, ok := paginate(c)
	if !ok {
		return
	}
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
		h.bindErrResponse(c, err)
		return
	}
	if rejectTooLong(c,
		lengthLimit{"name", req.Name, 255},
		lengthLimit{"description", req.Description, 2000},
	) {
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

type moduleUsageResp struct {
	WorkflowID          uuid.UUID            `json:"workflow_id"`
	WorkflowKey         string               `json:"workflow_key"`
	WorkflowName        string               `json:"workflow_name"`
	VersionID           uuid.UUID            `json:"version_id"`
	VersionNumber       *int32               `json:"version_number"`
	Status              domain.VersionStatus `json:"status"`
	ModuleVersionNumber int32                `json:"module_version_number"`
	Behind              bool                 `json:"behind"`
}

func toModuleUsageResp(u *domain.ModuleUsage) moduleUsageResp {
	return moduleUsageResp{
		WorkflowID:          u.WorkflowID,
		WorkflowKey:         u.WorkflowKey,
		WorkflowName:        u.WorkflowName,
		VersionID:           u.VersionID,
		VersionNumber:       u.VersionNumber,
		Status:              u.Status,
		ModuleVersionNumber: u.ModuleVersionNumber,
		Behind:              u.Behind,
	}
}

func (h *Handler) ListModuleUsages(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}
	moduleID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	usages, err := h.modules.Usages(c.Request.Context(), tenantID, moduleID)
	if err != nil {
		errResponse(c, h.log, err)
		return
	}
	resp := make([]moduleUsageResp, 0, len(usages))
	for _, u := range usages {
		resp = append(resp, toModuleUsageResp(u))
	}
	c.JSON(http.StatusOK, gin.H{"usages": resp})
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
	page, limit, ok := paginate(c)
	if !ok {
		return
	}

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

	if !h.requireModuleScopeAccess(c, tenantID, moduleID) {
		return
	}

	var req addModuleVersionReq
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		h.bindErrResponse(c, err)
		return
	}

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

	if !h.requireModuleScopeAccess(c, tenantID, moduleID) {
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

	if !h.requireModuleScopeAccess(c, tenantID, moduleID) {
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
