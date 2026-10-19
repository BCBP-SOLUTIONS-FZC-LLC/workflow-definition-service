package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func RequireJSONContentType() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !hasBody(c.Request) {
			c.Next()
			return
		}
		ct := c.GetHeader("Content-Type")
		if i := strings.IndexByte(ct, ';'); i >= 0 {
			ct = ct[:i]
		}
		if strings.TrimSpace(ct) != "application/json" {
			abortProblem(c, http.StatusUnsupportedMediaType, "https://api.workflow.platform/errors/unsupported-media-type",
				"Unsupported Media Type", "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json")
			return
		}
		c.Next()
	}
}

func hasBody(r *http.Request) bool {
	return r.ContentLength != 0
}
