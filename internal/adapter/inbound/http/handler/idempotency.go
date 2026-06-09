package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

const idempotencyTTL = 24 * time.Hour

type cachedResp struct {
	Status int    `json:"status"`
	Body   []byte `json:"body"`
}

type bodyRecorder struct {
	gin.ResponseWriter
	buf *bytes.Buffer
}

func (r *bodyRecorder) Write(b []byte) (int, error) {
	r.buf.Write(b)
	return r.ResponseWriter.Write(b) //nolint:wrapcheck
}

// WithIdempotency wraps a Gin handler with idempotency-key support.
//
// On the first call with a given Idempotency-Key header value, the handler runs
// normally and the response (status + body) is stored in Valkey under a
// tenant-scoped key with a 24-hour TTL.
//
// On subsequent requests with the same key from the same tenant, the cached
// response is returned without re-executing the handler. Only 2xx responses
// are cached; error responses are never replayed so callers can retry after
// fixing the request.
//
// If the cache is nil (dev/test) or the header is absent, the handler runs
// as-is with no idempotency enforcement.
func WithIdempotency(cache port.CacheStore, h gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("Idempotency-Key")
		if key == "" || cache == nil {
			h(c)
			return
		}

		rc, exists := gincommon.RequestContext(c)
		if !exists {
			h(c)
			return
		}

		cacheKey := "idem:" + rc.TenantID + ":" + key

		if raw, err := cache.Get(c.Request.Context(), cacheKey); err == nil {
			var cr cachedResp
			if json.Unmarshal([]byte(raw), &cr) == nil {
				c.Data(cr.Status, "application/json", cr.Body)
				return
			}
		}

		rec := &bodyRecorder{ResponseWriter: c.Writer, buf: &bytes.Buffer{}}
		c.Writer = rec
		h(c)

		status := rec.Status()
		if status >= http.StatusOK && status < http.StatusMultipleChoices {
			if b, err := json.Marshal(cachedResp{Status: status, Body: rec.buf.Bytes()}); err == nil {
				_ = cache.Set(c.Request.Context(), cacheKey, string(b), idempotencyTTL)
			}
		}
	}
}
