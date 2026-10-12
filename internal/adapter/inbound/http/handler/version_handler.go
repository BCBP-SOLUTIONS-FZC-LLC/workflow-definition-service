package handler

import (
	"encoding/json"
	"mime"
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
	ModuleRefs       []moduleRefResp       `json:"module_refs"`
	ArtifactHash     string                `json:"artifact_hash,omitempty"`
	IsValid          bool                  `json:"is_valid"`
	ValidationErrors []validationErrorItem `json:"validation_errors,omitempty"`
	CompiledPlanJSON json.RawMessage       `json:"compiled_plan_json,omitempty"`
	CreatedByUserID  uuid.UUID             `json:"created_by_user_id"`
	PublishedAt      *time.Time            `json:"published_at,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
	RecordVersion    int64                 `json:"record_version"`
}

type moduleRefResp struct {
	ModuleID      uuid.UUID `json:"module_id"`
	VersionID     uuid.UUID `json:"version_id"`
	ProcessID     string    `json:"process_id"`
	VersionNumber int32     `json:"version_number"`
}

func toModuleRefResp(ref domain.ModuleRef) moduleRefResp {
	return moduleRefResp{ModuleID: ref.ModuleID, VersionID: ref.VersionID, ProcessID: ref.ProcessID, VersionNumber: ref.VersionNumber}
}

func toVersionResp(v *domain.WorkflowVersion) versionResp {
	refs := make([]moduleRefResp, 0, len(v.ModuleRefs))
	for _, ref := range v.ModuleRefs {
		refs = append(refs, toModuleRefResp(ref))
	}
	r := versionResp{
		ID:              v.ID,
		WorkflowID:      v.WorkflowID,
		Status:          v.Status,
		VersionNumber:   v.VersionNumber,
		BPMNXML:         v.BPMNXML,
		ModuleRefs:      refs,
		ArtifactHash:    v.ArtifactHash,
		IsValid:         v.IsValid,
		CreatedByUserID: v.CreatedByUserID,
		PublishedAt:     v.PublishedAt,
		CreatedAt:       v.CreatedAt,
		UpdatedAt:       v.UpdatedAt,
		RecordVersion:   v.RecordVersion,
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
		errResponse(c, h.log, err)
		return
	}

	resp := make([]versionSummary, len(versions))
	for i, v := range versions {
		resp[i] = toVersionSummary(v)
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
		errResponse(c, h.log, err)
		return
	}

	vr := toVersionResp(v)
	c.JSON(http.StatusOK, vr)
}

func (h *Handler) PublishVersion(c *gin.Context) {
	tenantID, userID, ok := mustAdminCtx(c)
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
	_ = c.ShouldBindJSON(&req)

	v, err := h.versions.Publish(c.Request.Context(), tenantID, userID, workflowID, versionID, req.ForcePublishStructural)
	if err != nil {
		errResponse(c, h.log, err)
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
	tenantID, userID, ok := mustAdminCtx(c)
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

	var invalids []invalidParam
	if len(req.NewKey) > 255 {
		invalids = append(invalids, invalidParam{Name: "new_key", Reason: "must not exceed 255 characters"})
	}
	if len(req.NewName) > 255 {
		invalids = append(invalids, invalidParam{Name: "new_name", Reason: "must not exceed 255 characters"})
	}
	if len(invalids) > 0 {
		writeProblem(c, http.StatusUnprocessableEntity, CodeInvalidInput, "request fields exceed maximum length", invalids)
		return
	}

	wf, version, err := h.versions.Clone(c.Request.Context(), tenantID, userID, workflowID, versionID, c.GetHeader("x-plan"), service.CloneReq{
		NewKey:         req.NewKey,
		NewName:        req.NewName,
		NewDescription: req.NewDescription,
	})
	if err != nil {
		errResponse(c, h.log, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"workflow_id":    wf.ID,
		"version_id":     version.ID,
		"status":         version.Status,
		"version_number": version.VersionNumber,
		"message":        "Version cloned",
	})
}

func (h *Handler) PromoteVersion(c *gin.Context) {
	tenantID, userID, ok := mustAdminCtx(c)
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
		errResponse(c, h.log, err)
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
		errResponse(c, h.log, err)
		return
	}

	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
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
		errResponse(c, h.log, err)
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
			"stage_changes":       toStageChangeResps(result.Changes.StageChanges),
			"metadata_only":       result.Changes.MetadataOnly,
		},
	})
}

type stageChangeResp struct {
	NodeKey     string `json:"node_key"`
	Description string `json:"description"`
}

func toStageChangeResps(changes []service.StageChange) []stageChangeResp {
	out := make([]stageChangeResp, len(changes))
	for i, c := range changes {
		out[i] = stageChangeResp{NodeKey: c.NodeKey, Description: c.Description}
	}
	return out
}
