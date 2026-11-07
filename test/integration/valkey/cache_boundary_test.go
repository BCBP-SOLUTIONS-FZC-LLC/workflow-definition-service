//go:build integration

package valkey_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/valkey"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

const cacheBoundaryNoExpiry = time.Duration(-1)

func newCacheBoundaryStore(t *testing.T) (*valkey.Cache, *redis.Client) {
	t.Helper()
	client := fixtures.NewTestValkeyClient(t)
	require.NoError(t, client.Ping(context.Background()).Err())
	return valkey.NewCache(client), client
}

func TestCache_ZeroTTL_StoresKeyWithoutExpiry(t *testing.T) {
	ctx := context.Background()
	cache, client := newCacheBoundaryStore(t)

	cases := []struct {
		name    string
		key     string
		ttl     time.Duration
		write   func(key string, ttl time.Duration) error
		wantTTL func(t *testing.T, got time.Duration)
	}{
		{
			name: "Set with zero ttl",
			key:  "boundary:set:zero",
			ttl:  0,
			write: func(key string, ttl time.Duration) error {
				return cache.Set(ctx, key, "v", ttl)
			},
			wantTTL: func(t *testing.T, got time.Duration) {
				assert.Equal(t, cacheBoundaryNoExpiry, got)
			},
		},
		{
			name: "SetNX with zero ttl",
			key:  "boundary:setnx:zero",
			ttl:  0,
			write: func(key string, ttl time.Duration) error {
				ok, err := cache.SetNX(ctx, key, "v", ttl)
				if err == nil && !ok {
					return fmt.Errorf("SetNX on an absent key returned false")
				}
				return err
			},
			wantTTL: func(t *testing.T, got time.Duration) {
				assert.Equal(t, cacheBoundaryNoExpiry, got)
			},
		},
		{
			name: "Set with positive ttl expires",
			key:  "boundary:set:positive",
			ttl:  time.Minute,
			write: func(key string, ttl time.Duration) error {
				return cache.Set(ctx, key, "v", ttl)
			},
			wantTTL: func(t *testing.T, got time.Duration) {
				assert.Positive(t, got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, tc.write(tc.key, tc.ttl))

			got, err := cache.Get(ctx, tc.key)
			require.NoError(t, err)
			assert.Equal(t, "v", got)

			ttl, err := client.TTL(ctx, tc.key).Result()
			require.NoError(t, err)
			tc.wantTTL(t, ttl)
		})
	}
}

func TestCache_DelWithNoKeys_ReturnsWrappedServerError(t *testing.T) {
	ctx := context.Background()
	cache, client := newCacheBoundaryStore(t)
	require.NoError(t, cache.Set(ctx, "boundary:del:survivor", "v", time.Minute))

	err := cache.Del(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "valkey del: ")
	assert.Contains(t, err.Error(), "wrong number of arguments")

	remaining, getErr := client.Get(ctx, "boundary:del:survivor").Result()
	require.NoError(t, getErr)
	assert.Equal(t, "v", remaining)
}
