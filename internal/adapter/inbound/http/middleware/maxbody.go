package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const maxBodyBytes = 10 << 20 // 10 MB

// LimitRequestBody rejects request bodies larger than 10 MB with 413 before
// they are read by a handler. It wraps c.Request.Body with http.MaxBytesReader
// so that any read exceeding the limit returns *http.MaxBytesError, which the
// handler error mapper translates to PAYLOAD_TOO_LARGE.
func LimitRequestBody() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		c.Next()
	}
}
