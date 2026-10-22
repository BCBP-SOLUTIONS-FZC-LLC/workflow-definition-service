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
			abortProblem(c, http.StatusUnauthorized, "https://api.workflow.platform/errors/unauthorized",
				"Unauthorized", "UNAUTHORIZED", "missing or invalid "+InternalTokenHeader)
			return
		}
		c.Next()
	}
}
