package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memCache struct {
	mu    sync.Mutex
	items map[string]string
}

func newMemCache() *memCache { return &memCache{items: map[string]string{}} }

func (m *memCache) Get(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.items[key], nil
}

func (m *memCache) Set(_ context.Context, key, value string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[key] = value
	return nil
}

func (m *memCache) Del(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.items, k)
	}
	return nil
}

func (m *memCache) SetNX(_ context.Context, key, value string, _ time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, taken := m.items[key]; taken {
		return false, nil
	}
	m.items[key] = value
	return true, nil
}

func (m *memCache) DelIfEqual(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.items[key] == value {
		delete(m.items, key)
	}
	return nil
}

func (m *memCache) Ping(context.Context) error { return nil }

func idemReq(key string, userID uuid.UUID) *http.Request {
	r := req(http.MethodPost, "/idempotency-test", map[string]string{"name": "x"})
	r.Header.Set("Idempotency-Key", key)
	r.Header.Set("x-user-id", userID.String())
	return r
}

func newMemRouter(cache *memCache, h gin.HandlerFunc) *gin.Engine {
	return newIdempotencyRouter((*fakeCache)(nil).wrap(cache), h)
}

func (*fakeCache) wrap(m *memCache) *fakeCache {
	return &fakeCache{
		get:        m.Get,
		set:        m.Set,
		del:        m.Del,
		setnx:      m.SetNX,
		delIfEqual: m.DelIfEqual,
	}
}

func TestWithIdempotency_ConcurrentFirstRequests_RunOnce(t *testing.T) {
	cache := newMemCache()
	entered := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var once, releaseOnce sync.Once
	called := 0
	h := func(c *gin.Context) {
		mu.Lock()
		called++
		mu.Unlock()
		once.Do(func() { close(entered) })
		<-release
		c.JSON(http.StatusCreated, gin.H{"id": "1"})
	}
	r := newMemRouter(cache, h)
	user := uuid.New()

	first := make(chan int, 1)
	go func() { first <- do(r, idemReq("k", user)).Code }()
	<-entered

	secondDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { secondDone <- do(r, idemReq("k", user)) }()
	var second *httptest.ResponseRecorder
	select {
	case second = <-secondDone:
	case <-time.After(300 * time.Millisecond):
		releaseOnce.Do(func() { close(release) })
		second = <-secondDone
	}
	assert.Equal(t, http.StatusConflict, second.Code, "a request with a key that is still running must be refused, not run twice")
	assert.Contains(t, second.Body.String(), "IDEMPOTENCY_REQUEST_IN_PROGRESS")

	releaseOnce.Do(func() { close(release) })
	assert.Equal(t, http.StatusCreated, <-first)

	replay := do(r, idemReq("k", user))
	assert.Equal(t, http.StatusCreated, replay.Code)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, called, "the handler ran once; the retry was served from the stored response")
}

func TestWithIdempotency_AnotherUsersReplay_DoesNotReceiveTheStoredResponse(t *testing.T) {
	cache := newMemCache()
	called := 0
	h := func(c *gin.Context) {
		called++
		c.JSON(http.StatusCreated, gin.H{"n": called})
	}
	r := newMemRouter(cache, h)

	require.Equal(t, http.StatusCreated, do(r, idemReq("shared", uuid.New())).Code)
	other := do(r, idemReq("shared", uuid.New()))

	assert.Equal(t, 2, called, "the second user's request runs its own handler, which makes its own authorisation decision")
	assert.Equal(t, http.StatusCreated, other.Code)
}

func TestWithIdempotency_KeyOverTheLimit_Is400(t *testing.T) {
	called := 0
	r := newMemRouter(newMemCache(), func(c *gin.Context) { called++; c.Status(http.StatusOK) })

	w := do(r, idemReq(strings.Repeat("k", 257), uuid.New()))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, 0, called)

	assert.Equal(t, http.StatusOK, do(r, idemReq(strings.Repeat("k", 256), uuid.New())).Code, "256 characters is allowed")
}

func TestWithIdempotency_AFailedAttempt_ReleasesTheKeyForTheRetry(t *testing.T) {
	cache := newMemCache()
	attempts := 0
	h := func(c *gin.Context) {
		attempts++
		if attempts == 1 {
			c.JSON(http.StatusInternalServerError, gin.H{"code": "INTERNAL_ERROR"})
			return
		}
		c.JSON(http.StatusCreated, gin.H{"id": "1"})
	}
	r := newMemRouter(cache, h)
	user := uuid.New()

	assert.Equal(t, http.StatusInternalServerError, do(r, idemReq("retry", user)).Code)
	assert.Equal(t, http.StatusCreated, do(r, idemReq("retry", user)).Code, "a failed attempt must not block the client's retry")
	assert.Empty(t, pendingKeys(cache), "no pending marker is left behind")
}

func pendingKeys(m *memCache) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for k := range m.items {
		if strings.HasSuffix(k, ":pending") {
			keys = append(keys, k)
		}
	}
	return keys
}
