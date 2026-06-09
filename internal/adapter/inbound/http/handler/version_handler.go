package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type publishVersionReq struct {
	ForcePublishStructural bool `json:"force_publish_structural"`
}

type cloneVersionReq struct {
	NewKey         string `json:"new_key"         binding:"required"`
	NewName        string `json:"new_name"        binding:"required"`
	NewDescription string `json:"new_description"`
}

type validationErrorItem struct {
	NodeID string `json:"node_id"`
	Error  string `json:"error"`
}

type versionResp struct {
	ID               uuid.UUID             `json:"id"`
	WorkflowID       uuid.UUID             `json:"workflow_id"`
	Status           domain.VersionStatus  `json:"status"`
	VersionNumber    *int32                `json:"version_number,omitempty"`
	BPMNXML          string                `json:"bpmn_xml"`
	ArtifactHash     string                `json:"artifact_hash,omitempty"`
	IsValid          bool                  `json:"is_valid"`
	ValidationErrors []validationErrorItem `json:"validation_errors,omitempty"`
	CompiledPlanJSON json.RawMessage       `json:"compiled_plan_json,omitempty"`
	CreatedByUserID  uuid.UUID             `json:"created_by_user_id"`
	PublishedAt      *time.Time            `json:"published_at,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
}

func toVersionResp(v *domain.WorkflowVersion) versionResp {
	r := versionResp{
		ID:              v.ID,
		WorkflowID:      v.WorkflowID,
		Status:          v.Status,
		VersionNumber:   v.VersionNumber,
		BPMNXML:         v.BPMNXML,
		ArtifactHash:    v.ArtifactHash,
		IsValid:         v.IsValid,
		CreatedByUserID: v.CreatedByUserID,
		PublishedAt:     v.PublishedAt,
		CreatedAt:       v.CreatedAt,
		UpdatedAt:       v.UpdatedAt,
	}
	if v.CompiledPlanJSON != nil && *v.CompiledPlanJSON != "" {
		r.CompiledPlanJSON = json.RawMessage(*v.CompiledPlanJSON)
	}
	if v.ValidationErrorsJSON != nil && *v.ValidationErrorsJSON != "" {
		_ = json.Unmarshal([]byte(*v.ValidationErrorsJSON), &r.ValidationErrors)
	}
	return r
}

func (h *Handler) ListVersions(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}

	workflowID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	page, limit := paginate(c)

	versions, total, err := h.versions.List(c.Request.Context(), tenantID, workflowID, page, limit)
	if err != nil {
		errResponse(c, err)
		return
	}

	resp := make([]versionResp, len(versions))
	for i, v := range versions {
		resp[i] = toVersionResp(v)
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

func (h *Handler) GetVersion(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}

	workflowID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}

	v, err := h.versions.Get(c.Request.Context(), tenantID, workflowID, versionID)
	if err != nil {
		errResponse(c, err)
		return
	}

	vr := toVersionResp(v)
	c.JSON(http.StatusOK, vr)
}

func (h *Handler) PublishVersion(c *gin.Context) {
	tenantID, userID, ok := mustCtx(c)
	if !ok {
		return
	}

	workflowID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}

	var req publishVersionReq
	_ = c.ShouldBindJSON(&req) // body is optional; default forcePublishStructural=false

	v, err := h.versions.Publish(c.Request.Context(), tenantID, userID, workflowID, versionID, req.ForcePublishStructural)
	if err != nil {
		errResponse(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"workflow_id":    workflowID,
		"version_id":     v.ID,
		"status":         v.Status,
		"version_number": v.VersionNumber,
		"message":        "Version published",
	})
}

func (h *Handler) CloneVersion(c *gin.Context) {
	tenantID, userID, ok := mustCtx(c)
	if !ok {
		return
	}

	workflowID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}

	var req cloneVersionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindErrResponse(c, err)
		return
	}

	wf, version, err := h.versions.Clone(c.Request.Context(), tenantID, userID, workflowID, versionID, c.GetHeader("x-plan"), service.CloneReq{
		NewKey:         req.NewKey,
		NewName:        req.NewName,
		NewDescription: req.NewDescription,
	})
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

func (h *Handler) PromoteVersion(c *gin.Context) {
	tenantID, userID, ok := mustCtx(c)
	if !ok {
		return
	}

	workflowID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}

	promoted, err := h.versions.Promote(c.Request.Context(), tenantID, userID, workflowID, versionID)
	if err != nil {
		errResponse(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"workflow_id":       workflowID,
		"active_version_id": versionID,
		"version_number":    promoted.VersionNumber,
		"message":           "Version promoted to active",
	})
}

func (h *Handler) ExportBPMN(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}

	workflowID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}

	bpmnXML, filename, err := h.versions.Export(c.Request.Context(), tenantID, workflowID, versionID)
	if err != nil {
		errResponse(c, err)
		return
	}

	c.Header("Content-Disposition", "attachment; filename=\""+filename+"\"")
	c.Data(http.StatusOK, "application/xml; charset=utf-8", []byte(bpmnXML))
}

func (h *Handler) GetVersionDiff(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}

	workflowID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	baseVersionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}

	targetVersionID, ok := parseUUIDParam(c, "target_version_id")
	if !ok {
		return
	}

	result, err := h.versions.Diff(c.Request.Context(), tenantID, workflowID, baseVersionID, targetVersionID)
	if err != nil {
		errResponse(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"workflow_id":       result.WorkflowID,
		"base_version_id":   result.BaseVersionID,
		"target_version_id": result.TargetVersionID,
		"change_type":       result.ChangeType,
		"changes": gin.H{
			"added_departments":   result.Changes.AddedDepartments,
			"removed_departments": result.Changes.RemovedDepartments,
			"step_changes":        result.Changes.StepChanges,
			"metadata_only":       result.Changes.MetadataOnly,
		},
	})
}
