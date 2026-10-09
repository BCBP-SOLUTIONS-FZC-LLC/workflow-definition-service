package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
)

const InternalTokenHeader = "x-internal-token"

func RequireInternalToken(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token == "" {
			c.Next()
			return
		}
		if subtle.ConstantTimeCompare([]byte(c.GetHeader(InternalTokenHeader)), []byte(token)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"type":     "https://api.workflow.platform/errors/unauthorized",
				"title":    "Unauthorized",
				"status":   http.StatusUnauthorized,
				"detail":   "missing or invalid " + InternalTokenHeader,
				"instance": c.Request.URL.Path,
				"code":     "UNAUTHORIZED",
			})
			return
		}
		c.Next()
	}
}
