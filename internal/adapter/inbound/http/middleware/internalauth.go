package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
)

const InternalTokenHeader = "x-internal-token"

// RequireInternalToken guards the internal route group. When token is empty
// the check is disabled (local/dev); otherwise a request whose x-internal-token
// does not match is rejected with 401 and an RFC-9457 problem body. The
// compare is constant-time so the token cannot be recovered byte by byte.
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
