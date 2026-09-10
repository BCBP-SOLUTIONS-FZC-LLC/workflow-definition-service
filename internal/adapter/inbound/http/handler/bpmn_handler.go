package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/enums"
)

func (h *Handler) AllowedBPMNElements(c *gin.Context) {
	_, _, ok := mustCtx(c)
	if !ok {
		return
	}

	c.JSON(http.StatusOK, gin.H{"elements": enums.AllowedBPMNElements})
}
