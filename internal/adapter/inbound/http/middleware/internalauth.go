package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const InternalTokenHeader = "x-internal-token"

// RequireInternalToken guards the internal route group (e.g. POST /internal/events).
// When token is empty the check is disabled (local/dev) and requests pass through;
// NetworkPolicy / service mesh remains the primary control. When token is set, a
// request missing or mismatching the x-internal-token header is rejected with 401.
func RequireInternalToken(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token == "" {
			c.Next()
			return
		}
		if c.GetHeader(InternalTokenHeader) != token {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	}
}
