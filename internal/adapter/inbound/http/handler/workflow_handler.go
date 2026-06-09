package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type createWorkflowReq struct {
	Key         string `json:"key"      binding:"required"`
	Name        string `json:"name"     binding:"required"`
	Description string `json:"description"`
	BPMNXML     string `json:"bpmn_xml" binding:"required"`
}

type workflowResp struct {
	ID                  uuid.UUID  `json:"id"`
	Key                 string     `json:"key"`
	Name                string     `json:"name"`
	Description         string     `json:"description"`
	ActiveVersionID     *uuid.UUID `json:"active_version_id,omitempty"`
	ActiveVersionNumber *int32     `json:"active_version_number,omitempty"`
	HasDraft            bool       `json:"has_draft"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type versionSummary struct {
	ID              uuid.UUID            `json:"id"`
	Status          domain.VersionStatus `json:"status"`
	VersionNumber   *int32               `json:"version_number,omitempty"`
	IsValid         bool                 `json:"is_valid"`
	CreatedByUserID uuid.UUID            `json:"created_by_user_id"`
	PublishedAt     *time.Time           `json:"published_at,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
}

func toWorkflowResp(w *domain.Workflow) workflowResp {
	return workflowResp{
		ID:                  w.ID,
		Key:                 w.BusinessKey,
		Name:                w.Name,
		Description:         w.Description,
		ActiveVersionID:     w.ActiveVersionID,
		ActiveVersionNumber: w.ActiveVersionNumber,
		HasDraft:            w.HasDraft,
		CreatedAt:           w.CreatedAt,
		UpdatedAt:           w.UpdatedAt,
	}
}

func toVersionSummary(v *domain.WorkflowVersion) versionSummary {
	return versionSummary{
		ID:              v.ID,
		Status:          v.Status,
		VersionNumber:   v.VersionNumber,
		IsValid:         v.IsValid,
		CreatedByUserID: v.CreatedByUserID,
		PublishedAt:     v.PublishedAt,
		CreatedAt:       v.CreatedAt,
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
	if k := c.Query("key"); k != "" {
		filter.BusinessKey = &k
	}
	if v := c.Query("is_valid"); v != "" {
		b := v == "true"
		filter.IsValid = &b
	}
	if v := c.Query("has_draft"); v != "" {
		b := v == "true"
		filter.HasDraft = &b
	}
	if s := c.Query("status"); s != "" {
		archived := s == "archived"
		filter.Archived = &archived
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
		"pagination": gin.H{
			"total_count": total,
			"page":        page,
			"limit":       limit,
		},
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
		req.Key, req.Name, req.Description, req.BPMNXML,
	)
	if err != nil {
		errResponse(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"workflow_id":    wf.ID,
		"version_id":     version.ID,
		"status":         version.Status,
		"version_number": version.VersionNumber,
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

	versionsLimit, _ := strconv.Atoi(c.DefaultQuery("versions_limit", "20"))

	wf, versions, err := h.workflows.Get(c.Request.Context(), tenantID, workflowID, versionsLimit)
	if err != nil {
		errResponse(c, err)
		return
	}

	summaries := make([]versionSummary, len(versions))
	for i, v := range versions {
		summaries[i] = toVersionSummary(v)
	}
	wr := toWorkflowResp(wf)
	c.JSON(http.StatusOK, gin.H{
		"id":                    wr.ID,
		"key":                   wr.Key,
		"name":                  wr.Name,
		"description":           wr.Description,
		"active_version_id":     wr.ActiveVersionID,
		"active_version_number": wr.ActiveVersionNumber,
		"has_draft":             wr.HasDraft,
		"created_at":            wr.CreatedAt,
		"updated_at":            wr.UpdatedAt,
		"versions":              summaries,
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

	c.JSON(http.StatusOK, gin.H{
		"workflow_id": workflowID,
		"status":      "ARCHIVED",
		"message":     "Workflow archived successfully",
	})
}
