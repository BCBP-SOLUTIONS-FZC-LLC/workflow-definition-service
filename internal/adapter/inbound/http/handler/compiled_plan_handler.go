package handler

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type compiledPlanResp struct {
	WorkflowID    uuid.UUID       `json:"workflow_id"`
	VersionID     uuid.UUID       `json:"version_id"`
	VersionNumber *int32          `json:"version_number"`
	Status        string          `json:"status"`
	IsValid       bool            `json:"is_valid"`
	CompiledPlan  json.RawMessage `json:"compiled_plan"`
}

func toCompiledPlanResp(p *service.CompiledPlan) compiledPlanResp {
	r := compiledPlanResp{
		WorkflowID: p.WorkflowID, VersionID: p.VersionID, VersionNumber: p.VersionNumber,
		Status: string(p.Status), IsValid: p.IsValid,
	}
	if p.PlanJSON != nil {
		r.CompiledPlan = json.RawMessage(*p.PlanJSON)
	}
	return r
}

func (h *Handler) GetCompiledPlan(c *gin.Context) {
	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}
	tenantID, err := uuid.Parse(c.Query("tenant_id"))
	if err != nil {
		writeProblem(c, http.StatusBadRequest, CodeBadRequest, "tenant_id must be a UUID", nil)
		return
	}
	ctx := pgcommon.WithGUCSet(c.Request.Context(), pgdomain.GUCSet{TenantID: tenantID.String()})
	p, err := h.compiledPlans.Get(ctx, tenantID, versionID)
	if err != nil {
		errResponse(c, h.log, err)
		return
	}
	c.JSON(http.StatusOK, toCompiledPlanResp(p))
}
