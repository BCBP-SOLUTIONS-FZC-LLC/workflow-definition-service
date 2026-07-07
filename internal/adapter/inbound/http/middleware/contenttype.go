package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// RequireJSONContentType rejects a request that carries a body whose Content-Type
// is not application/json with 415 UNSUPPORTED_MEDIA_TYPE (RFC-9457 problem body).
// Body-less requests (GET/DELETE, or empty-body POSTs such as archive/promote)
// pass through untouched, so bodyless mutations are unaffected.
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
			c.AbortWithStatusJSON(http.StatusUnsupportedMediaType, gin.H{
				"type":     "https://api.workflow.platform/errors/unsupported-media-type",
				"title":    "Unsupported Media Type",
				"status":   http.StatusUnsupportedMediaType,
				"detail":   "Content-Type must be application/json",
				"instance": c.Request.URL.Path,
				"code":     "UNSUPPORTED_MEDIA_TYPE",
			})
			return
		}
		c.Next()
	}
}

// hasBody reports whether the request may carry a body: a positive Content-Length,
// or -1 (unknown / chunked transfer). Content-Length 0 means no body.
func hasBody(r *http.Request) bool {
	return r.ContentLength != 0
}
