package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Workflows tag — listWorkflows, createWorkflow, getWorkflow, archiveWorkflow

func (h *Handler) ListWorkflows(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, ProblemDetails{
		Type:     errBase + "not-implemented",
		Title:    "Not Implemented",
		Status:   http.StatusNotImplemented,
		Detail:   "listWorkflows is not yet implemented",
		Instance: c.Request.URL.Path,
		Code:     CodeInternal,
	})
}

func (h *Handler) CreateWorkflow(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, ProblemDetails{
		Type:     errBase + "not-implemented",
		Title:    "Not Implemented",
		Status:   http.StatusNotImplemented,
		Detail:   "createWorkflow is not yet implemented",
		Instance: c.Request.URL.Path,
		Code:     CodeInternal,
	})
}

func (h *Handler) GetWorkflow(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, ProblemDetails{
		Type:     errBase + "not-implemented",
		Title:    "Not Implemented",
		Status:   http.StatusNotImplemented,
		Detail:   "getWorkflow is not yet implemented",
		Instance: c.Request.URL.Path,
		Code:     CodeInternal,
	})
}

func (h *Handler) ArchiveWorkflow(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, ProblemDetails{
		Type:     errBase + "not-implemented",
		Title:    "Not Implemented",
		Status:   http.StatusNotImplemented,
		Detail:   "archiveWorkflow is not yet implemented",
		Instance: c.Request.URL.Path,
		Code:     CodeInternal,
	})
}
