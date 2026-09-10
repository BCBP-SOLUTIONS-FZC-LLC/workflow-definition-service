package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
)

// fakeCache implements port.CacheStore for idempotency tests.
type fakeCache struct {
	get   func(ctx context.Context, key string) (string, error)
	set   func(ctx context.Context, key string, value string, ttl time.Duration) error
	del   func(ctx context.Context, keys ...string) error
	setnx func(ctx context.Context, key string, value string, ttl time.Duration) (bool, error)
	ping  func(ctx context.Context) error
}

func (f *fakeCache) Get(ctx context.Context, key string) (string, error) {
	if f.get != nil {
		return f.get(ctx, key)
	}
	return "", assert.AnError
}
func (f *fakeCache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if f.set != nil {
		return f.set(ctx, key, value, ttl)
	}
	return nil
}
func (f *fakeCache) Del(ctx context.Context, keys ...string) error {
	if f.del != nil {
		return f.del(ctx, keys...)
	}
	return nil
}
func (f *fakeCache) SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	if f.setnx != nil {
		return f.setnx(ctx, key, value, ttl)
	}
	return true, nil
}
func (f *fakeCache) Ping(ctx context.Context) error {
	if f.ping != nil {
		return f.ping(ctx)
	}
	return nil
}

// echoHandler is a simple handler that writes statusCode and a JSON body.
func echoHandler(statusCode int, body gin.H) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(statusCode, body)
	}
}

func newIdempotencyRouter(cache *fakeCache, h gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := newRouter(newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{}))
	// Overlay a dedicated route that wraps h with idempotency middleware.
	r.POST("/idempotency-test", handler.WithIdempotency(cache, nil, 24*time.Hour, h))
	return r
}

func TestWithIdempotency_NoHeader_PassThrough(t *testing.T) {
	called := 0
	h := func(c *gin.Context) {
		called++
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
	r := newIdempotencyRouter(&fakeCache{}, h)

	w := do(r, req(http.MethodPost, "/idempotency-test", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, called)
}

func TestWithIdempotency_NilCache_PassThrough(t *testing.T) {
	called := 0
	h := func(c *gin.Context) {
		called++
		c.JSON(http.StatusCreated, gin.H{"ok": true})
	}

	gin.SetMode(gin.TestMode)
	r := newRouter(newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{}))
	r.POST("/idempotency-test", handler.WithIdempotency(nil, nil, 24*time.Hour, h))

	httpReq := req(http.MethodPost, "/idempotency-test", nil)
	httpReq.Header.Set("Idempotency-Key", "key-abc")
	w := do(r, httpReq)
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, 1, called)
}

func TestWithIdempotency_CacheMiss_HandlerRunsAndCaches(t *testing.T) {
	called := 0
	var storedKey, storedVal string

	cache := &fakeCache{
		get: func(_ context.Context, _ string) (string, error) { return "", assert.AnError },
		set: func(_ context.Context, key, val string, _ time.Duration) error {
			storedKey = key
			storedVal = val
			return nil
		},
	}

	h := func(c *gin.Context) {
		called++
		c.JSON(http.StatusCreated, gin.H{"id": "123"})
	}
	r := newIdempotencyRouter(cache, h)

	httpReq := req(http.MethodPost, "/idempotency-test", nil)
	httpReq.Header.Set("Idempotency-Key", "my-key")
	w := do(r, httpReq)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, 1, called)
	require.Contains(t, storedKey, "my-key")
	assert.NotEmpty(t, storedVal)
}

func TestWithIdempotency_CacheHit_Replay(t *testing.T) {
	called := 0

	// Build a valid cachedResp JSON as stored by WithIdempotency.
	type cachedResp struct {
		Status int    `json:"status"`
		Body   []byte `json:"body"`
	}
	cachedBody := []byte(`{"cached":true}`)
	cached, _ := json.Marshal(cachedResp{Status: http.StatusCreated, Body: cachedBody})

	cache := &fakeCache{
		get: func(_ context.Context, _ string) (string, error) { return string(cached), nil },
	}

	h := func(c *gin.Context) {
		called++ // should NOT be called on replay
		c.JSON(http.StatusOK, gin.H{"fresh": true})
	}
	r := newIdempotencyRouter(cache, h)

	httpReq := req(http.MethodPost, "/idempotency-test", nil)
	httpReq.Header.Set("Idempotency-Key", "repeat-key")
	w := do(r, httpReq)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, 0, called, "handler must not be called on replay")
	assert.JSONEq(t, `{"cached":true}`, w.Body.String())
}

func TestWithIdempotency_ErrorResponse_NotCached(t *testing.T) {
	setCalled := false

	cache := &fakeCache{
		get: func(_ context.Context, _ string) (string, error) { return "", assert.AnError },
		set: func(_ context.Context, _, _ string, _ time.Duration) error {
			setCalled = true
			return nil
		},
	}

	h := echoHandler(http.StatusBadRequest, gin.H{"error": "bad"})
	r := newIdempotencyRouter(cache, h)

	httpReq := req(http.MethodPost, "/idempotency-test", nil)
	httpReq.Header.Set("Idempotency-Key", "err-key")
	w := do(r, httpReq)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.False(t, setCalled, "error responses must not be cached")
}

func TestWithIdempotency_HashMismatch_Returns409(t *testing.T) {
	// Cached entry has a body_hash that doesn't match the incoming request body.
	type cachedResp struct {
		Status   int    `json:"status"`
		Body     []byte `json:"body"`
		BodyHash string `json:"body_hash,omitempty"`
	}
	cachedBody := []byte(`{"cached":true}`)
	cached, _ := json.Marshal(cachedResp{
		Status:   http.StatusCreated,
		Body:     cachedBody,
		BodyHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})

	cache := &fakeCache{
		get: func(_ context.Context, _ string) (string, error) { return string(cached), nil },
	}

	called := 0
	h := func(c *gin.Context) {
		called++
		c.JSON(http.StatusCreated, gin.H{"fresh": true})
	}
	r := newIdempotencyRouter(cache, h)

	// Different body → hash will differ from stored "aaaa..." hash.
	httpReq := req(http.MethodPost, "/idempotency-test", map[string]string{"different": "payload"})
	httpReq.Header.Set("Idempotency-Key", "conflict-key")
	w := do(r, httpReq)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, 0, called, "handler must not be called on hash mismatch")
}

func TestWithIdempotency_PathScopedKey_IsolatedPerRoute(t *testing.T) {
	// Same Idempotency-Key on two different routes must produce distinct cache keys.
	var storedKeys []string

	cache := &fakeCache{
		get: func(_ context.Context, _ string) (string, error) { return "", assert.AnError },
		set: func(_ context.Context, key, _ string, _ time.Duration) error {
			storedKeys = append(storedKeys, key)
			return nil
		},
	}

	gin.SetMode(gin.TestMode)
	r := newRouter(newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{}))
	r.POST("/route-a", handler.WithIdempotency(cache, nil, 24*time.Hour, echoHandler(http.StatusCreated, gin.H{"r": "a"})))
	r.POST("/route-b", handler.WithIdempotency(cache, nil, 24*time.Hour, echoHandler(http.StatusCreated, gin.H{"r": "b"})))

	for _, path := range []string{"/route-a", "/route-b"} {
		httpReq := req(http.MethodPost, path, map[string]string{"x": "1"})
		httpReq.Header.Set("Idempotency-Key", "same-key")
		do(r, httpReq)
	}

	require.Len(t, storedKeys, 2)
	assert.NotEqual(t, storedKeys[0], storedKeys[1], "cache keys must differ between routes")
	assert.Contains(t, storedKeys[0], "/route-a")
	assert.Contains(t, storedKeys[1], "/route-b")
}

func TestWithIdempotency_UnmarshalFailure_TreatedAsMiss(t *testing.T) {
	called := 0

	cache := &fakeCache{
		get: func(_ context.Context, _ string) (string, error) { return "not-valid-json", nil },
		set: func(_ context.Context, _, _ string, _ time.Duration) error { return nil },
	}

	h := func(c *gin.Context) {
		called++
		c.JSON(http.StatusCreated, gin.H{"ok": true})
	}
	r := newIdempotencyRouter(cache, h)

	httpReq := req(http.MethodPost, "/idempotency-test", nil)
	httpReq.Header.Set("Idempotency-Key", "bad-json-key")
	w := do(r, httpReq)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, 1, called, "handler must be called when cached entry cannot be parsed")
}

func TestWithIdempotency_CacheHit_NoHash_Replay(t *testing.T) {
	// Cached entry with no body_hash — response is replayed regardless of incoming body.
	type cachedResp struct {
		Status int    `json:"status"`
		Body   []byte `json:"body"`
	}
	cachedBody := []byte(`{"legacy":true}`)
	cached, _ := json.Marshal(cachedResp{Status: http.StatusCreated, Body: cachedBody})

	cache := &fakeCache{
		get: func(_ context.Context, _ string) (string, error) { return string(cached), nil },
	}

	called := 0
	h := func(c *gin.Context) {
		called++
		c.JSON(http.StatusOK, gin.H{"fresh": true})
	}
	r := newIdempotencyRouter(cache, h)

	httpReq := req(http.MethodPost, "/idempotency-test", map[string]string{"any": "body"})
	httpReq.Header.Set("Idempotency-Key", "no-hash-key")
	w := do(r, httpReq)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, 0, called, "handler must not be called when cache hit has no hash")
	assert.JSONEq(t, `{"legacy":true}`, w.Body.String())
}

func TestWithIdempotency_CacheSetFails_ResponseStillReturned(t *testing.T) {
	called := 0

	cache := &fakeCache{
		get: func(_ context.Context, _ string) (string, error) { return "", assert.AnError },
		set: func(_ context.Context, _, _ string, _ time.Duration) error { return assert.AnError },
	}

	h := func(c *gin.Context) {
		called++
		c.JSON(http.StatusCreated, gin.H{"id": "abc"})
	}
	r := newIdempotencyRouter(cache, h)

	httpReq := req(http.MethodPost, "/idempotency-test", nil)
	httpReq.Header.Set("Idempotency-Key", "set-fail-key")
	w := do(r, httpReq)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, 1, called, "handler must still be called even when cache.Set fails")
}

func TestWithIdempotency_NoRequestContext_PassThrough(t *testing.T) {
	// Router without ProtectedMiddlewares — gincommon.RequestContext will be absent.
	// WithIdempotency must fall through to the handler rather than panicking.
	called := 0
	h := func(c *gin.Context) {
		called++
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}

	gin.SetMode(gin.TestMode)
	r := gin.New() // no ProtectedMiddlewares
	r.POST("/bare", handler.WithIdempotency(&fakeCache{}, nil, 24*time.Hour, h))

	httpReq, _ := http.NewRequest(http.MethodPost, "/bare", nil)
	httpReq.Header.Set("Idempotency-Key", "any-key")
	w := do(r, httpReq)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, called)
}

func TestWithIdempotency_DrainBodyError_PassThrough(t *testing.T) {
	// Simulate an unreadable request body; drainBody returns ok=false.
	// The middleware must log a warning (when logger is non-nil) and still call the handler.
	called := 0
	logger := &fakeLogger{}

	cache := &fakeCache{
		get: func(_ context.Context, _ string) (string, error) { return "", assert.AnError },
	}
	h := func(c *gin.Context) {
		called++
		c.JSON(http.StatusCreated, gin.H{"ok": true})
	}

	gin.SetMode(gin.TestMode)
	r := newRouter(newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{}))
	r.POST("/drain-test", handler.WithIdempotency(cache, logger, 24*time.Hour, h))

	httpReq := req(http.MethodPost, "/drain-test", nil)
	httpReq.Body = io.NopCloser(errReader{})
	httpReq.Header.Set("Idempotency-Key", "drain-key")
	w := do(r, httpReq)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, 1, called, "handler must be called even when body read fails")
	require.Len(t, logger.warnCalls, 1, "expected one warn log for body read failure")
	assert.Contains(t, logger.warnCalls[0], "failed to read request body")
}

func TestWithIdempotency_CacheSetFails_LogsWarning(t *testing.T) {
	// cache.Set fails after a successful handler run; a warn must be logged when logger is non-nil.
	called := 0
	logger := &fakeLogger{}

	cache := &fakeCache{
		get: func(_ context.Context, _ string) (string, error) { return "", assert.AnError },
		set: func(_ context.Context, _, _ string, _ time.Duration) error { return assert.AnError },
	}
	h := func(c *gin.Context) {
		called++
		c.JSON(http.StatusCreated, gin.H{"id": "xyz"})
	}

	gin.SetMode(gin.TestMode)
	r := newRouter(newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{}))
	r.POST("/set-warn-test", handler.WithIdempotency(cache, logger, 24*time.Hour, h))

	httpReq := req(http.MethodPost, "/set-warn-test", nil)
	httpReq.Header.Set("Idempotency-Key", "warn-key")
	w := do(r, httpReq)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, 1, called)
	require.Len(t, logger.warnCalls, 1, "expected one warn log for cache set failure")
	assert.Contains(t, logger.warnCalls[0], "failed to cache response")
}
