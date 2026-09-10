package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type validateBPMNReq struct {
	BPMNXML        string   `json:"bpmn_xml" binding:"required"`
	ModuleBPMNXMLs []string `json:"module_bpmn_xmls"`
}

type bpmnIssueResp struct {
	NodeID   string `json:"node_id"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

func (h *Handler) ValidateBPMN(c *gin.Context) {
	_, _, ok := mustCtx(c)
	if !ok {
		return
	}

	var req validateBPMNReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindErrResponse(c, err)
		return
	}

	isValid, errs, err := h.validation.Validate(c.Request.Context(), req.BPMNXML, req.ModuleBPMNXMLs)
	if err != nil {
		h.logForbiddenXML(c, err)
		errResponse(c, h.log, err)
		return
	}

	issues := make([]bpmnIssueResp, len(errs))
	for i, e := range errs {
		issues[i] = bpmnIssueResp{
			NodeID:   e.NodeID,
			Code:     string(e.Code),
			Message:  e.Message,
			Severity: string(e.Severity),
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"is_valid": isValid,
		"issues":   issues,
	})
}
