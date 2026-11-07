package handler_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithIdempotency_StoreFailure_LogsTheHashedKeyNotTheKey(t *testing.T) {
	const rawKey = "store-failure-client-key"
	log := &w2aFieldLogger{}
	cache := &fakeCache{
		get: func(context.Context, string) (string, error) { return "", nil },
		set: func(context.Context, string, string, time.Duration) error {
			return errors.New("cache refused the write")
		},
	}
	r := w2aRouter(cache, log, func(c *gin.Context) { c.JSON(http.StatusCreated, gin.H{"id": "1"}) })

	require.Equal(t, http.StatusCreated, do(r, w2aKeyedRequest(rawKey, testUserID)).Code)

	var warns []w2aWarnEntry
	for _, w := range log.snapshot() {
		if strings.Contains(w.msg, "failed to cache response") {
			warns = append(warns, w)
		}
	}
	require.Len(t, warns, 1)
	fields := warns[0].fields
	assert.NotContains(t, fields, "cache_key", "the full key must not be logged")
	hash, ok := fields["cache_key_hash"].(string)
	require.True(t, ok, "the warning carries a cache_key_hash field")
	assert.Regexp(t, "^[0-9a-f]{16}$", hash)
	assert.Equal(t, "cache refused the write", fields["error"])
	for field, v := range fields {
		s, _ := v.(string)
		assert.NotContains(t, s, rawKey, "field %s must not carry the client's key", field)
		assert.NotContains(t, s, testTenantID.String(), "field %s must not carry the tenant id", field)
		assert.NotContains(t, s, testUserID.String(), "field %s must not carry the user id", field)
	}
}
