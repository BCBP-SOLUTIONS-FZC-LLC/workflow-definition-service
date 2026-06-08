package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type updateDraftReq struct {
	Name          *string    `json:"name"`
	Description   *string    `json:"description"`
	BPMNXML       *string    `json:"bpmn_xml"`
	LastUpdatedAt *time.Time `json:"last_updated_at"`
}

type draftResp struct {
	ID           uuid.UUID            `json:"id"`
	WorkflowID   uuid.UUID            `json:"workflow_id"`
	Status       domain.VersionStatus `json:"status"`
	BPMNXML      string               `json:"bpmn_xml"`
	IsValid      bool                 `json:"is_valid"`
	ArtifactHash string               `json:"artifact_hash,omitempty"`
	CreatedAt    time.Time            `json:"created_at"`
	UpdatedAt    time.Time            `json:"updated_at"`
}

func toDraftResp(v *domain.WorkflowVersion) draftResp {
	return draftResp{
		ID:           v.ID,
		WorkflowID:   v.WorkflowID,
		Status:       v.Status,
		BPMNXML:      v.BPMNXML,
		IsValid:      v.IsValid,
		ArtifactHash: v.ArtifactHash,
		CreatedAt:    v.CreatedAt,
		UpdatedAt:    v.UpdatedAt,
	}
}

func (h *Handler) GetDraft(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}

	workflowID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	draft, err := h.drafts.Get(c.Request.Context(), tenantID, workflowID)
	if err != nil {
		errResponse(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"draft": toDraftResp(draft)})
}

func (h *Handler) InitDraft(c *gin.Context) {
	tenantID, userID, ok := mustCtx(c)
	if !ok {
		return
	}

	workflowID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	draft, err := h.drafts.Init(c.Request.Context(), tenantID, userID, workflowID)
	if err != nil {
		errResponse(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"draft": toDraftResp(draft)})
}

func (h *Handler) UpdateDraft(c *gin.Context) {
	tenantID, userID, ok := mustCtx(c)
	if !ok {
		return
	}

	workflowID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	var req updateDraftReq
	if err := c.ShouldBindJSON(&req); err != nil {
		writeProblem(c, http.StatusBadRequest, CodeBadRequest, err.Error(), nil)
		return
	}

	draft, err := h.drafts.Update(c.Request.Context(), tenantID, userID, workflowID, service.UpdateDraftReq{
		Name:          req.Name,
		Description:   req.Description,
		BPMNXML:       req.BPMNXML,
		LastUpdatedAt: req.LastUpdatedAt,
	})
	if err != nil {
		errResponse(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"draft": toDraftResp(draft)})
}

func (h *Handler) DiscardDraft(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}

	workflowID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}

	if err := h.drafts.Discard(c.Request.Context(), tenantID, workflowID); err != nil {
		errResponse(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
