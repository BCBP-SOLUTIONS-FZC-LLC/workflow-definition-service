package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type validateBPMNReq struct {
	BPMNXML string `json:"bpmn_xml" binding:"required"`
}

type bpmnIssueResp struct {
	NodeID   string `json:"node_id"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

func (h *Handler) ValidateBPMN(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}

	var req validateBPMNReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindErrResponse(c, err)
		return
	}

	isValid, errs, err := h.validation.Validate(c.Request.Context(), tenantID, req.BPMNXML)
	if err != nil {
		h.logForbiddenXML(c, err)
		errResponse(c, h.log, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"is_valid": isValid,
		"issues":   toBPMNIssueResps(errs),
	})
}

func toBPMNIssueResps(errs []domain.BPMNValidationError) []bpmnIssueResp {
	issues := make([]bpmnIssueResp, len(errs))
	for i, e := range errs {
		issues[i] = bpmnIssueResp{
			NodeID:   e.NodeID,
			Code:     string(e.Code),
			Message:  e.Message,
			Severity: string(e.Severity),
		}
	}
	return issues
}

// GetVersionValidation re-runs the structural validator against a stored
// version's own BPMN, in any status, and writes nothing. A validator rule added
// after a version was published does not retroactively invalidate it, so this is
// how an operator finds a template that still holds a defect rather than
// discovering it when the Execution Service refuses to instantiate (§3.3.12.1).
func (h *Handler) GetVersionValidation(c *gin.Context) {
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

	isValid, errs, err := h.validation.ValidateVersion(c.Request.Context(), tenantID, v)
	if err != nil {
		h.logForbiddenXML(c, err)
		errResponse(c, h.log, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"version_id": v.ID,
		"status":     v.Status,
		"is_valid":   isValid,
		"issues":     toBPMNIssueResps(errs),
	})
}
