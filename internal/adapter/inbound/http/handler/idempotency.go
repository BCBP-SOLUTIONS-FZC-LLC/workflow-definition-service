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

const idempotencyTTL = 24 * time.Hour

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

// WithIdempotency wraps a Gin handler with idempotency-key support.
//
// On the first call with a given Idempotency-Key the handler runs normally and
// the response (status + body) plus a SHA-256 hash of the request body are
// stored in Valkey under a tenant-scoped key with a 24-hour TTL.
//
// On subsequent requests with the same key:
//   - If the request body hash matches the stored hash, the cached response is
//     returned without re-executing the handler (standard idempotent replay).
//   - If the hash differs, the request is rejected with IDEMPOTENCY_KEY_REPLAY
//     (409) so callers know they reused a key with a different payload.
//
// Only 2xx responses are cached; error responses are never replayed so callers
// can retry after fixing the request.
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

		bodyBytes, incomingHash, ok := drainBody(c)
		if !ok {
			h(c)
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		cacheKey := "idem:" + rc.TenantID + ":" + key
		if replayed := replayIfCached(c, cache, cacheKey, incomingHash); replayed {
			return
		}

		rec := &bodyRecorder{ResponseWriter: c.Writer, buf: &bytes.Buffer{}}
		c.Writer = rec
		h(c)
		storeIfSuccess(c, cache, cacheKey, incomingHash, rec)
	}
}

func drainBody(c *gin.Context) (body []byte, hash string, ok bool) {
	b, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, "", false
	}
	return b, hashBody(b), true
}

// replayIfCached checks the cache for a prior response. Returns true when the
// request was handled (either replayed or rejected as a hash mismatch).
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
		errResponse(c, domain.ErrIdempotencyKeyReplay)
		return true
	}
	c.Data(cr.Status, "application/json", cr.Body)
	return true
}

func storeIfSuccess(c *gin.Context, cache port.CacheStore, cacheKey, incomingHash string, rec *bodyRecorder) {
	status := rec.Status()
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return
	}
	entry := cachedResp{Status: status, Body: rec.buf.Bytes(), BodyHash: incomingHash}
	if b, err := json.Marshal(entry); err == nil {
		_ = cache.Set(c.Request.Context(), cacheKey, string(b), idempotencyTTL)
	}
}
