package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type cachedResp struct {
	Status   int    `json:"status"`
	Body     []byte `json:"body"`
	BodyHash string `json:"body_hash,omitempty"`
}

type bodyRecorder struct {
	gin.ResponseWriter
	buf *bytes.Buffer
}

func (r *bodyRecorder) Write(b []byte) (int, error) {
	r.buf.Write(b)
	return r.ResponseWriter.Write(b) //nolint:wrapcheck
}

func hashBody(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (h *Handler) Idempotent(fn gin.HandlerFunc) gin.HandlerFunc {
	return WithIdempotency(h.cache, h.log, h.idempotencyTTL, fn)
}

func WithIdempotency(cache port.CacheStore, log port.Logger, ttl time.Duration, h gin.HandlerFunc) gin.HandlerFunc {
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

		bodyBytes, incomingHash, ok := drainBody(c)
		if !ok {
			if log != nil {
				log.Warn("idempotency: failed to read request body, bypassing idempotency protection", map[string]any{
					"tenant_id": rc.TenantID,
					"path":      c.Request.URL.Path,
				})
			}
			h(c)
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		cacheKey := idempotencyCacheKey(rc.TenantID, c.Request.Method, c.Request.URL.Path, key)
		if replayed := replayIfCached(c, cache, cacheKey, incomingHash); replayed {
			return
		}

		rec := &bodyRecorder{ResponseWriter: c.Writer, buf: &bytes.Buffer{}}
		c.Writer = rec
		h(c)
		storeIfSuccess(c, cache, log, cacheKey, incomingHash, ttl, rec)
	}
}

func idempotencyCacheKey(tenantID, method, path, key string) string {
	return "idem:" + tenantID + ":" + method + ":" + path + ":" + key
}

func drainBody(c *gin.Context) (body []byte, hash string, ok bool) {
	b, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, "", false
	}
	return b, hashBody(b), true
}

func replayIfCached(c *gin.Context, cache port.CacheStore, cacheKey, incomingHash string) bool {
	raw, err := cache.Get(c.Request.Context(), cacheKey)
	if err != nil {
		return false
	}
	var cr cachedResp
	if json.Unmarshal([]byte(raw), &cr) != nil {
		return false
	}
	if cr.BodyHash != "" && cr.BodyHash != incomingHash {
		errResponse(c, nil, domain.ErrIdempotencyKeyReplay)
		return true
	}
	c.Data(cr.Status, "application/json", cr.Body)
	return true
}

func storeIfSuccess(c *gin.Context, cache port.CacheStore, log port.Logger, cacheKey, incomingHash string, ttl time.Duration, rec *bodyRecorder) {
	status := rec.Status()
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return
	}
	entry := cachedResp{Status: status, Body: rec.buf.Bytes(), BodyHash: incomingHash}
	b, _ := json.Marshal(entry)
	if err := cache.Set(c.Request.Context(), cacheKey, string(b), ttl); err != nil {
		if log != nil {
			log.Warn("idempotency: failed to cache response", map[string]any{
				"cache_key": cacheKey,
				"error":     err.Error(),
			})
		}
	}
}
