package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type createWorkflowReq struct {
	BusinessKey string `json:"business_key" binding:"required"`
	Name        string `json:"name"         binding:"required"`
	Description string `json:"description"`
	BPMNXML     string `json:"bpmn_xml"     binding:"required"`
}

type workflowResp struct {
	ID              uuid.UUID  `json:"id"`
	BusinessKey     string     `json:"business_key"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	ActiveVersionID *uuid.UUID `json:"active_version_id,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type versionSummary struct {
	ID            uuid.UUID            `json:"id"`
	Status        domain.VersionStatus `json:"status"`
	VersionNumber *int32               `json:"version_number,omitempty"`
	IsValid       bool                 `json:"is_valid"`
	CreatedAt     time.Time            `json:"created_at"`
}

func toWorkflowResp(w *domain.Workflow) workflowResp {
	return workflowResp{
		ID:              w.ID,
		BusinessKey:     w.BusinessKey,
		Name:            w.Name,
		Description:     w.Description,
		ActiveVersionID: w.ActiveVersionID,
		CreatedAt:       w.CreatedAt,
		UpdatedAt:       w.UpdatedAt,
	}
}

func toVersionSummary(v *domain.WorkflowVersion) versionSummary {
	return versionSummary{
		ID:            v.ID,
		Status:        v.Status,
		VersionNumber: v.VersionNumber,
		IsValid:       v.IsValid,
		CreatedAt:     v.CreatedAt,
	}
}

func (h *Handler) ListWorkflows(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}

	page, limit := paginate(c)
	filter := port.WorkflowFilter{Page: page, Limit: limit}

	if s := c.Query("search"); s != "" {
		filter.Search = &s
	}
	if bk := c.Query("business_key"); bk != "" {
		filter.BusinessKey = &bk
	}
	if v := c.Query("is_valid"); v != "" {
		b := v == "true"
		filter.IsValid = &b
	}
	if v := c.Query("has_draft"); v != "" {
		b := v == "true"
		filter.HasDraft = &b
	}
	if v := c.Query("archived"); v != "" {
		b := v == "true"
		filter.Archived = &b
	}

	workflows, total, err := h.workflows.List(c.Request.Context(), tenantID, filter)
	if err != nil {
		errResponse(c, err)
		return
	}

	resp := make([]workflowResp, len(workflows))
	for i, w := range workflows {
		resp[i] = toWorkflowResp(w)
	}
	c.JSON(http.StatusOK, gin.H{
		"workflows": resp,
		"total":     total,
		"page":      page,
		"limit":     limit,
	})
}

func (h *Handler) CreateWorkflow(c *gin.Context) {
	tenantID, userID, ok := mustCtx(c)
	if !ok {
		return
	}

	var req createWorkflowReq
	if err := c.ShouldBindJSON(&req); err != nil {
		writeProblem(c, http.StatusBadRequest, CodeBadRequest, err.Error(), nil)
		return
	}

	wf, version, err := h.workflows.Create(
		c.Request.Context(),
		tenantID, userID,
		req.BusinessKey, req.Name, req.Description, req.BPMNXML,
	)
	if err != nil {
		errResponse(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"workflow": toWorkflowResp(wf),
		"version":  toVersionSummary(version),
	})
}

func (h *Handler) GetWorkflow(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}

	workflowID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	wf, versions, err := h.workflows.Get(c.Request.Context(), tenantID, workflowID)
	if err != nil {
		errResponse(c, err)
		return
	}

	summaries := make([]versionSummary, len(versions))
	for i, v := range versions {
		summaries[i] = toVersionSummary(v)
	}
	c.JSON(http.StatusOK, gin.H{
		"workflow": toWorkflowResp(wf),
		"versions": summaries,
	})
}

func (h *Handler) ArchiveWorkflow(c *gin.Context) {
	tenantID, userID, ok := mustCtx(c)
	if !ok {
		return
	}

	workflowID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	if err := h.workflows.Archive(c.Request.Context(), tenantID, userID, workflowID); err != nil {
		errResponse(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
