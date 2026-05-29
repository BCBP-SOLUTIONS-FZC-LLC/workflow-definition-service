package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Validation tag — validateBpmn (stateless, no DB writes)

func (h *Handler) ValidateBPMN(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, ProblemDetails{
		Type:     errBase + "not-implemented",
		Title:    "Not Implemented",
		Status:   http.StatusNotImplemented,
		Detail:   "validateBpmn is not yet implemented",
		Instance: c.Request.URL.Path,
		Code:     CodeInternal,
	})
}
