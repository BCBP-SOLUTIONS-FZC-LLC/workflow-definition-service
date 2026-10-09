package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const maxBodyBytes = 10 << 20

func LimitRequestBody() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		c.Next()
	}
}
