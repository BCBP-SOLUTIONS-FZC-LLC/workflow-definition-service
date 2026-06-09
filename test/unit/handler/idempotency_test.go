package handler_test

import (
	"context"
	"encoding/json"
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
	r.POST("/idempotency-test", handler.WithIdempotency(cache, h))
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
	r.POST("/idempotency-test", handler.WithIdempotency(nil, h))

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
