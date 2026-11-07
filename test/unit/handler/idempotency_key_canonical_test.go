package handler_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithIdempotency_AnotherSpellingOfTheSameIDs_IsTheSameKey(t *testing.T) {
	canonicalTenant := testTenantID.String()
	canonicalUser := testUserID.String()
	tests := []struct {
		name   string
		tenant string
		user   string
	}{
		{"upper case", strings.ToUpper(canonicalTenant), strings.ToUpper(canonicalUser)},
		{"braces", "{" + canonicalTenant + "}", "{" + canonicalUser + "}"},
		{"no dashes", strings.ReplaceAll(canonicalTenant, "-", ""), strings.ReplaceAll(canonicalUser, "-", "")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newMemCache()
			called := 0
			h := func(c *gin.Context) {
				called++
				c.JSON(http.StatusCreated, gin.H{"n": called})
			}
			r := newMemRouter(cache, h)

			first := do(r, idemReq("same-key", testUserID))
			require.Equal(t, http.StatusCreated, first.Code)

			variant := idemReq("same-key", testUserID)
			variant.Header.Set("x-tenant-id", tt.tenant)
			variant.Header.Set("x-user-id", tt.user)
			second := do(r, variant)

			assert.Equal(t, http.StatusCreated, second.Code)
			assert.Equal(t, 1, called, "the same ids in another spelling must replay the stored answer, not run the handler again")
			assert.JSONEq(t, first.Body.String(), second.Body.String())
		})
	}
}

func TestWithIdempotency_CacheKeyHoldsTheCanonicalIDs(t *testing.T) {
	var keys []string
	cache := &fakeCache{
		get: func(context.Context, string) (string, error) { return "", nil },
		setnx: func(_ context.Context, key, _ string, _ time.Duration) (bool, error) {
			keys = append(keys, key)
			return true, nil
		},
	}
	r := newIdempotencyRouter(cache, func(c *gin.Context) { c.JSON(http.StatusCreated, gin.H{"id": "1"}) })

	httpReq := idemReq("canon", testUserID)
	httpReq.Header.Set("x-tenant-id", "{"+testTenantID.String()+"}")
	require.Equal(t, http.StatusCreated, do(r, httpReq).Code)

	require.Len(t, keys, 1)
	assert.Equal(t, "idem:"+testTenantID.String()+":"+testUserID.String()+":POST:/idempotency-test:canon:pending", keys[0])
}
