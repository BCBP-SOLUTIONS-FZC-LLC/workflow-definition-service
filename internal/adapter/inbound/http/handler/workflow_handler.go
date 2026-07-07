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

type getWorkflowResp struct {
	workflowResp
	Versions []versionSummary `json:"versions"`
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
		errResponse(c, h.log, err)
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
		bindErrResponse(c, err)
		return
	}

	var invalids []invalidParam
	if len(req.Key) > 255 {
		invalids = append(invalids, invalidParam{Name: "key", Reason: "must not exceed 255 characters"})
	}
	if len(req.Name) > 255 {
		invalids = append(invalids, invalidParam{Name: "name", Reason: "must not exceed 255 characters"})
	}
	if len(req.Description) > 2000 {
		invalids = append(invalids, invalidParam{Name: "description", Reason: "must not exceed 2000 characters"})
	}
	if len(invalids) > 0 {
		writeProblem(c, http.StatusUnprocessableEntity, CodeInvalidInput, "request fields exceed maximum length", invalids)
		return
	}

	wf, version, err := h.workflows.Create(
		c.Request.Context(),
		tenantID, userID,
		req.Key, req.Name, req.Description, req.BPMNXML, normalizePlanTier(c.GetHeader("x-plan")),
	)
	if err != nil {
		h.logForbiddenXML(c, err)
		errResponse(c, h.log, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"workflow_id":    wf.ID,
		"version_id":     version.ID,
		"status":         version.Status,
		"version_number": version.VersionNumber,
		"message":        "Workflow created",
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

	versionsLimit := boundedQuery(c, "versions_limit", 20, 1, 100)

	wf, versions, err := h.workflows.Get(c.Request.Context(), tenantID, workflowID, versionsLimit)
	if err != nil {
		errResponse(c, h.log, err)
		return
	}

	summaries := make([]versionSummary, len(versions))
	for i, v := range versions {
		summaries[i] = toVersionSummary(v)
	}
	c.JSON(http.StatusOK, getWorkflowResp{
		workflowResp: toWorkflowResp(wf),
		Versions:     summaries,
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
		errResponse(c, h.log, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"workflow_id": workflowID,
		"status":      "ARCHIVED",
		"message":     "Workflow archived successfully",
	})
}

// normalizePlanTier maps unrecognised x-plan header values to "starter" so the
// quota service always receives a canonical tier string.
// TODO: return 400 for unknown plan once membership service can validate plan tier.
func normalizePlanTier(plan string) string {
	switch plan {
	case "starter", "pro", "enterprise":
		return plan
	default:
		return "starter"
	}
}
