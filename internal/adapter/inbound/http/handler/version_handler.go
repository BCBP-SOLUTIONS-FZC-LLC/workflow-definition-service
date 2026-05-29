package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Versions tag — listWorkflowVersions, getVersion, publishVersion,
//                cloneVersion, promoteVersion, exportBpmn, getVersionDiff

func (h *Handler) ListVersions(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, ProblemDetails{
		Type:     errBase + "not-implemented",
		Title:    "Not Implemented",
		Status:   http.StatusNotImplemented,
		Detail:   "listWorkflowVersions is not yet implemented",
		Instance: c.Request.URL.Path,
		Code:     CodeInternal,
	})
}

func (h *Handler) GetVersion(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, ProblemDetails{
		Type:     errBase + "not-implemented",
		Title:    "Not Implemented",
		Status:   http.StatusNotImplemented,
		Detail:   "getVersion is not yet implemented",
		Instance: c.Request.URL.Path,
		Code:     CodeInternal,
	})
}

func (h *Handler) PublishVersion(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, ProblemDetails{
		Type:     errBase + "not-implemented",
		Title:    "Not Implemented",
		Status:   http.StatusNotImplemented,
		Detail:   "publishVersion is not yet implemented",
		Instance: c.Request.URL.Path,
		Code:     CodeInternal,
	})
}

func (h *Handler) CloneVersion(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, ProblemDetails{
		Type:     errBase + "not-implemented",
		Title:    "Not Implemented",
		Status:   http.StatusNotImplemented,
		Detail:   "cloneVersion is not yet implemented",
		Instance: c.Request.URL.Path,
		Code:     CodeInternal,
	})
}

func (h *Handler) PromoteVersion(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, ProblemDetails{
		Type:     errBase + "not-implemented",
		Title:    "Not Implemented",
		Status:   http.StatusNotImplemented,
		Detail:   "promoteVersion is not yet implemented",
		Instance: c.Request.URL.Path,
		Code:     CodeInternal,
	})
}

func (h *Handler) ExportBPMN(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, ProblemDetails{
		Type:     errBase + "not-implemented",
		Title:    "Not Implemented",
		Status:   http.StatusNotImplemented,
		Detail:   "exportBpmn is not yet implemented",
		Instance: c.Request.URL.Path,
		Code:     CodeInternal,
	})
}

func (h *Handler) GetVersionDiff(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, ProblemDetails{
		Type:     errBase + "not-implemented",
		Title:    "Not Implemented",
		Status:   http.StatusNotImplemented,
		Detail:   "getVersionDiff is not yet implemented",
		Instance: c.Request.URL.Path,
		Code:     CodeInternal,
	})
}
