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

type createStarterReq struct {
	Name        string `json:"name"        binding:"required"`
	Description string `json:"description"`
	Category    string `json:"category"`
	BPMNXML     string `json:"bpmn_xml"    binding:"required"`
	Scope       string `json:"scope"`
}

type createStarterFromWorkflowVersionReq struct {
	Name        string `json:"name"        binding:"required"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

type starterResp struct {
	ID                      uuid.UUID  `json:"id"`
	TenantID                *uuid.UUID `json:"tenant_id,omitempty"`
	Scope                   string     `json:"scope"`
	Name                    string     `json:"name"`
	Description             string     `json:"description"`
	Category                string     `json:"category,omitempty"`
	BPMNXML                 string     `json:"bpmn_xml"`
	SourceWorkflowVersionID *uuid.UUID `json:"source_workflow_version_id,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

func toStarterResp(s *domain.StarterTemplate) starterResp {
	return starterResp{
		ID:                      s.ID,
		TenantID:                s.TenantID,
		Scope:                   string(s.Scope),
		Name:                    s.Name,
		Description:             s.Description,
		Category:                s.Category,
		BPMNXML:                 s.BPMNXML,
		SourceWorkflowVersionID: s.SourceWorkflowVersionID,
		CreatedAt:               s.CreatedAt,
		UpdatedAt:               s.UpdatedAt,
	}
}

func (h *Handler) ListStarters(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}

	page, limit := paginate(c)
	filter := port.StarterFilter{Page: page, Limit: limit}
	scope, ok := parseScopeFilter(c, c.Query("scope"))
	if !ok {
		return
	}
	filter.Scope = scope
	if cat := c.Query("category"); cat != "" {
		filter.Category = &cat
	}
	if q := c.Query("q"); q != "" {
		filter.Search = &q
	}

	starters, total, err := h.starters.List(c.Request.Context(), tenantID, filter)
	if err != nil {
		errResponse(c, h.log, err)
		return
	}

	resp := make([]starterResp, len(starters))
	for i, s := range starters {
		resp[i] = toStarterResp(s)
	}
	c.JSON(http.StatusOK, gin.H{
		"starters": resp,
		"pagination": gin.H{
			"total_count": total,
			"page":        page,
			"limit":       limit,
		},
	})
}

func (h *Handler) CreateStarter(c *gin.Context) {
	tenantID, userID, ok := mustAdminCtx(c)
	if !ok {
		return
	}

	var req createStarterReq
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

	st, err := h.starters.Create(c.Request.Context(), tenantID, userID, service.CreateStarterReq{
		Name:        req.Name,
		Description: req.Description,
		Category:    req.Category,
		BPMNXML:     req.BPMNXML,
		Scope:       scope,
	})
	if err != nil {
		h.logForbiddenXML(c, err)
		errResponse(c, h.log, err)
		return
	}
	c.JSON(http.StatusCreated, toStarterResp(st))
}

func (h *Handler) CreateStarterFromWorkflowVersion(c *gin.Context) {
	tenantID, userID, ok := mustAdminCtx(c)
	if !ok {
		return
	}
	sourceVersionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}

	var req createStarterFromWorkflowVersionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindErrResponse(c, err)
		return
	}

	st, err := h.starters.CreateFromWorkflowVersion(c.Request.Context(), tenantID, userID, sourceVersionID,
		service.CreateFromWorkflowVersionReq{Name: req.Name, Description: req.Description, Category: req.Category})
	if err != nil {
		errResponse(c, h.log, err)
		return
	}
	c.JSON(http.StatusCreated, toStarterResp(st))
}

func (h *Handler) GetStarter(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	st, err := h.starters.Get(c.Request.Context(), tenantID, id)
	if err != nil {
		errResponse(c, h.log, err)
		return
	}
	c.JSON(http.StatusOK, toStarterResp(st))
}

func (h *Handler) DeleteStarter(c *gin.Context) {
	tenantID, _, ok := mustAdminCtx(c)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	st, err := h.starters.Get(c.Request.Context(), tenantID, id)
	if err != nil {
		errResponse(c, h.log, err)
		return
	}
	if st.Scope == domain.ScopeGlobal && !requirePlatformOperator(c) {
		return
	}

	if err := h.starters.Delete(c.Request.Context(), tenantID, id); err != nil {
		errResponse(c, h.log, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"starter_id": id,
		"message":    "Starter deleted",
	})
}
