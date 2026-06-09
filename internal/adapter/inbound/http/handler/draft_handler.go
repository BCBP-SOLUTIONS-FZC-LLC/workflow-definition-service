package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type updateDraftReq struct {
	Name          *string    `json:"name"`
	Description   *string    `json:"description"`
	BPMNXML       *string    `json:"bpmn_xml"`
	LastUpdatedAt *time.Time `json:"last_updated_at"`
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

	c.JSON(http.StatusOK, toVersionResp(draft))
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

	c.JSON(http.StatusCreated, gin.H{
		"workflow_id":    draft.WorkflowID,
		"version_id":     draft.ID,
		"status":         draft.Status,
		"version_number": draft.VersionNumber,
		"message":        "Draft initialized",
	})
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

	c.JSON(http.StatusOK, gin.H{
		"workflow_id":    draft.WorkflowID,
		"version_id":     draft.ID,
		"status":         draft.Status,
		"version_number": draft.VersionNumber,
		"message":        "Draft updated",
	})
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
