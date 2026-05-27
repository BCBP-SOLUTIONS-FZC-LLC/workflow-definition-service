package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func healthzHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "OK"})
}

func readyzHandler(c *gin.Context) {
	// TODO: add db ping check in next phase
	c.JSON(http.StatusOK, gin.H{"status": "OK"})
}
