package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type validateBPMNReq struct {
	BPMNXML string `json:"bpmn_xml" binding:"required"`
}

type bpmnErrorResp struct {
	NodeID  string `json:"node_id"`
	Code    string `json:"code"`
	Message string `json:"message"`
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

	isValid, errs, err := h.validation.Validate(c.Request.Context(), req.BPMNXML)
	if err != nil {
		errResponse(c, err)
		return
	}

	errResps := make([]bpmnErrorResp, len(errs))
	for i, e := range errs {
		errResps[i] = bpmnErrorResp{
			NodeID:  e.NodeID,
			Code:    string(e.Code),
			Message: e.Message,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"is_valid": isValid,
		"errors":   errResps,
	})
}
