package http

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed static/swagger-initializer.js
var swaggerInitializerJS []byte

func SwaggerInitializerHandler(c *gin.Context) {
	c.Header("Content-Type", "application/javascript; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "application/javascript; charset=utf-8", swaggerInitializerJS)
}
