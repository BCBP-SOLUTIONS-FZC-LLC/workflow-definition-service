//go:build integration

package valkey_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/valkey"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func TestCache_Del_RemovesEveryNamedKeyAndIgnoresAbsentOnes(t *testing.T) {
	ctx := context.Background()
	client := fixtures.NewTestValkeyClient(t)
	cache := valkey.NewCache(client)

	t.Run("several present keys and one absent key", func(t *testing.T) {
		for _, key := range []string{"w11d:del:a", "w11d:del:b", "w11d:del:keep"} {
			require.NoError(t, cache.Set(ctx, key, "v", time.Minute))
		}

		err := cache.Del(ctx, "w11d:del:a", "w11d:del:never-set", "w11d:del:b")

		require.NoError(t, err)
		left, existsErr := client.Exists(ctx, "w11d:del:a", "w11d:del:b", "w11d:del:keep").Result()
		require.NoError(t, existsErr)
		assert.EqualValues(t, 1, left, "only the key that was not named is left")
		kept, getErr := cache.Get(ctx, "w11d:del:keep")
		require.NoError(t, getErr)
		assert.Equal(t, "v", kept)
	})

	t.Run("only absent keys", func(t *testing.T) {
		require.NoError(t, cache.Del(ctx, "w11d:del:absent-1", "w11d:del:absent-2"))
	})
}

func TestCache_DelIfEqual_DeletesOnlyWhenTheStoredValueIsTheToken(t *testing.T) {
	ctx := context.Background()
	client := fixtures.NewTestValkeyClient(t)
	cache := valkey.NewCache(client)

	cases := []struct {
		name      string
		seed      bool
		token     string
		wantValue string
	}{
		{name: "same token deletes the key", seed: true, token: "token-mine", wantValue: ""},
		{name: "another holder's token leaves the key and its value", seed: true, token: "token-other", wantValue: "token-mine"},
		{name: "absent key stays absent", seed: false, token: "token-mine", wantValue: ""},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := "w11d:lock:" + string(rune('a'+i))
			if tc.seed {
				require.NoError(t, cache.Set(ctx, key, "token-mine", time.Minute))
			}

			err := cache.DelIfEqual(ctx, key, tc.token)

			require.NoError(t, err)
			got, getErr := cache.Get(ctx, key)
			require.NoError(t, getErr)
			assert.Equal(t, tc.wantValue, got)
			if tc.wantValue != "" {
				ttl, ttlErr := client.TTL(ctx, key).Result()
				require.NoError(t, ttlErr)
				assert.Positive(t, ttl, "the untouched key keeps its expiry")
			}
		})
	}
}
