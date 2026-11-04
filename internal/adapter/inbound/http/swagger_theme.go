package http

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed static/swagger-theme.css
var swaggerThemeCSS []byte

func SwaggerThemeHandler(c *gin.Context) {
	c.Header("Content-Type", "text/css; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/css; charset=utf-8", swaggerThemeCSS)
}
