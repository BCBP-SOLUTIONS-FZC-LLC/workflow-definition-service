package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type publishVersionReq struct {
	SkipEligibilityCheck bool `json:"skip_eligibility_check"`
}

type cloneVersionReq struct {
	NewKey         string `json:"new_key"         binding:"required"`
	NewName        string `json:"new_name"        binding:"required"`
	NewDescription string `json:"new_description"`
}

type versionResp struct {
	ID               uuid.UUID            `json:"id"`
	WorkflowID       uuid.UUID            `json:"workflow_id"`
	Status           domain.VersionStatus `json:"status"`
	VersionNumber    *int32               `json:"version_number,omitempty"`
	ArtifactHash     string               `json:"artifact_hash,omitempty"`
	IsValid          bool                 `json:"is_valid"`
	CompiledPlanJSON *string              `json:"compiled_plan_json,omitempty"`
	PublishedAt      *time.Time           `json:"published_at,omitempty"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
}

func toVersionResp(v *domain.WorkflowVersion) versionResp {
	return versionResp{
		ID:               v.ID,
		WorkflowID:       v.WorkflowID,
		Status:           v.Status,
		VersionNumber:    v.VersionNumber,
		ArtifactHash:     v.ArtifactHash,
		IsValid:          v.IsValid,
		CompiledPlanJSON: v.CompiledPlanJSON,
		PublishedAt:      v.PublishedAt,
		CreatedAt:        v.CreatedAt,
		UpdatedAt:        v.UpdatedAt,
	}
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
		"total":    total,
		"page":     page,
		"limit":    limit,
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

	c.JSON(http.StatusOK, gin.H{"version": toVersionResp(v)})
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
	_ = c.ShouldBindJSON(&req) // body is optional; default skip_eligibility_check=false

	v, err := h.versions.Publish(c.Request.Context(), tenantID, userID, workflowID, versionID, req.SkipEligibilityCheck)
	if err != nil {
		errResponse(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"version": toVersionResp(v)})
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
		writeProblem(c, http.StatusBadRequest, CodeBadRequest, err.Error(), nil)
		return
	}

	wf, version, err := h.versions.Clone(c.Request.Context(), tenantID, userID, workflowID, versionID, service.CloneReq{
		NewKey:         req.NewKey,
		NewName:        req.NewName,
		NewDescription: req.NewDescription,
	})
	if err != nil {
		errResponse(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"workflow": toWorkflowResp(wf),
		"version":  toVersionResp(version),
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

	if err := h.versions.Promote(c.Request.Context(), tenantID, userID, workflowID, versionID); err != nil {
		errResponse(c, err)
		return
	}

	c.Status(http.StatusNoContent)
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
