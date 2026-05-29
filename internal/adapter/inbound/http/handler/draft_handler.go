package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Drafts tag — getDraft, initDraft, updateDraft, discardDraft

func (h *Handler) GetDraft(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, ProblemDetails{
		Type:     errBase + "not-implemented",
		Title:    "Not Implemented",
		Status:   http.StatusNotImplemented,
		Detail:   "getDraft is not yet implemented",
		Instance: c.Request.URL.Path,
		Code:     CodeInternal,
	})
}

func (h *Handler) InitDraft(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, ProblemDetails{
		Type:     errBase + "not-implemented",
		Title:    "Not Implemented",
		Status:   http.StatusNotImplemented,
		Detail:   "initDraft is not yet implemented",
		Instance: c.Request.URL.Path,
		Code:     CodeInternal,
	})
}

func (h *Handler) UpdateDraft(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, ProblemDetails{
		Type:     errBase + "not-implemented",
		Title:    "Not Implemented",
		Status:   http.StatusNotImplemented,
		Detail:   "updateDraft is not yet implemented",
		Instance: c.Request.URL.Path,
		Code:     CodeInternal,
	})
}

func (h *Handler) DiscardDraft(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, ProblemDetails{
		Type:     errBase + "not-implemented",
		Title:    "Not Implemented",
		Status:   http.StatusNotImplemented,
		Detail:   "discardDraft is not yet implemented",
		Instance: c.Request.URL.Path,
		Code:     CodeInternal,
	})
}
