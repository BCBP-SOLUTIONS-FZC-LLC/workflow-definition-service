package handler_test

import (
	"context"
	"errors"
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

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
)

type w2aWarnEntry struct {
	msg    string
	fields map[string]any
}

type w2aFieldLogger struct {
	mu    sync.Mutex
	warns []w2aWarnEntry
}

func (l *w2aFieldLogger) Debug(string, map[string]any) {}
func (l *w2aFieldLogger) Info(string, map[string]any)  {}
func (l *w2aFieldLogger) Error(string, map[string]any) {}
func (l *w2aFieldLogger) Fatal(string, map[string]any) {}
func (l *w2aFieldLogger) Warn(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, w2aWarnEntry{msg: msg, fields: fields})
}

func (l *w2aFieldLogger) snapshot() []w2aWarnEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]w2aWarnEntry(nil), l.warns...)
}

func w2aKeyedRequest(key string, userID uuid.UUID) *http.Request {
	r := req(http.MethodPost, "/w2a-idempotent", map[string]string{"name": "x"})
	r.Header.Set("Idempotency-Key", key)
	r.Header.Set("x-user-id", userID.String())
	return r
}

func w2aRouter(cache *fakeCache, log *w2aFieldLogger, h gin.HandlerFunc) *gin.Engine {
	r := newRouter(newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{}))
	r.POST("/w2a-idempotent", handler.WithIdempotency(cache, log, time.Hour, h))
	return r
}

func TestWithIdempotency_ReservationStoreDown_BothConcurrentRequestsRunAndTheWarningHashesTheKey(t *testing.T) {
	const rawKey = "w2a-secret-client-key"
	user := uuid.New()
	log := &w2aFieldLogger{}

	var mu sync.Mutex
	var lockKeys []string
	var releases int
	cache := &fakeCache{
		get: func(context.Context, string) (string, error) { return "", nil },
		setnx: func(_ context.Context, key, _ string, _ time.Duration) (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			lockKeys = append(lockKeys, key)
			return false, errors.New("w2a connection refused to the cache")
		},
		delIfEqual: func(context.Context, string, string) error {
			mu.Lock()
			defer mu.Unlock()
			releases++
			return nil
		},
	}

	var arrivals sync.WaitGroup
	arrivals.Add(2)
	overlap := make(chan struct{})
	go func() {
		arrivals.Wait()
		close(overlap)
	}()
	var calls int
	h := func(c *gin.Context) {
		mu.Lock()
		calls++
		mu.Unlock()
		arrivals.Done()
		select {
		case <-overlap:
		case <-time.After(10 * time.Second):
		}
		c.JSON(http.StatusCreated, gin.H{"id": "created"})
	}
	r := w2aRouter(cache, log, h)

	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() { results <- do(r, w2aKeyedRequest(rawKey, user)) }()
	}
	first, second := <-results, <-results

	assert.Equal(t, http.StatusCreated, first.Code)
	assert.Equal(t, http.StatusCreated, second.Code)
	assert.Contains(t, first.Body.String(), "created")
	assert.Contains(t, second.Body.String(), "created")
	mu.Lock()
	assert.Equal(t, 2, calls, "with the reservation store down both requests with the same key execute")
	assert.Zero(t, releases, "no reservation was taken, so nothing is released")
	require.Len(t, lockKeys, 2)
	for _, k := range lockKeys {
		assert.True(t, strings.HasSuffix(k, ":pending"))
		assert.Contains(t, k, testTenantID.String(), "the reservation is scoped to the tenant")
		assert.Contains(t, k, user.String(), "the reservation is scoped to the user")
	}
	mu.Unlock()

	var reservationWarns []w2aWarnEntry
	for _, w := range log.snapshot() {
		if strings.Contains(w.msg, "reservation unavailable") {
			reservationWarns = append(reservationWarns, w)
		}
	}
	require.Len(t, reservationWarns, 2, "each request logs one warning for the failed reservation")
	for _, w := range reservationWarns {
		hash, ok := w.fields["cache_key_hash"].(string)
		require.True(t, ok, "the warning carries a cache_key_hash field")
		assert.Len(t, hash, 16)
		assert.Regexp(t, "^[0-9a-f]{16}$", hash)
		for field, v := range w.fields {
			s, _ := v.(string)
			assert.NotContains(t, s, rawKey, "field %s must not carry the client's key", field)
			assert.NotContains(t, s, testTenantID.String(), "field %s must not carry the tenant id", field)
		}
		assert.Equal(t, "w2a connection refused to the cache", w.fields["error"])
	}
}

func TestWithIdempotency_ReleaseAfterClientDisconnect_DeletesTheReservation(t *testing.T) {
	user := uuid.New()
	var mu sync.Mutex
	items := map[string]string{}
	var released []string
	cache := &fakeCache{
		get: func(context.Context, string) (string, error) { return "", nil },
		setnx: func(_ context.Context, key, value string, _ time.Duration) (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			if _, taken := items[key]; taken {
				return false, nil
			}
			items[key] = value
			return true, nil
		},
		delIfEqual: func(ctx context.Context, key, value string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			if items[key] == value {
				delete(items, key)
				released = append(released, key)
			}
			return nil
		},
	}

	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ranHandler bool
	h := func(c *gin.Context) {
		ranHandler = true
		mu.Lock()
		held := len(items)
		mu.Unlock()
		assert.Equal(t, 1, held, "the reservation is held while the handler runs")
		cancel()
		assert.Error(t, c.Request.Context().Err(), "the client disconnect cancels the request context")
		c.JSON(http.StatusCreated, gin.H{"id": "created"})
	}
	r := w2aRouter(cache, &w2aFieldLogger{}, h)

	do(r, w2aKeyedRequest("w2a-disconnect", user).WithContext(reqCtx))

	require.True(t, ranHandler)
	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, items, "the reservation is deleted even though the request context was cancelled")
	require.Len(t, released, 1)
	assert.True(t, strings.HasSuffix(released[0], ":pending"))
}
