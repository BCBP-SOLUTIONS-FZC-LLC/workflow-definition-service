package cache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/valkey"
)

func TestCache_ClientError_IsWrappedWithTheOperationAndKeepsTheCause(t *testing.T) {
	cause := errors.New("connection reset by peer")
	ctx := context.Background()

	cases := []struct {
		name       string
		client     *mockCmdable
		call       func(c *valkey.Cache) (any, error)
		wantPrefix string
		wantValue  any
	}{
		{
			name: "get returns no value even when the client sent one",
			client: &mockCmdable{getFunc: func(context.Context, string) *redis.StringCmd {
				return redis.NewStringResult("stale-value", cause)
			}},
			call:       func(c *valkey.Cache) (any, error) { return c.Get(ctx, "w11d:get") },
			wantPrefix: "valkey get: ",
			wantValue:  "",
		},
		{
			name: "setnx returns false even when the client said true",
			client: &mockCmdable{setNXFunc: func(context.Context, string, interface{}, time.Duration) *redis.BoolCmd {
				return redis.NewBoolResult(true, cause)
			}},
			call:       func(c *valkey.Cache) (any, error) { return c.SetNX(ctx, "w11d:setnx", "v", time.Minute) },
			wantPrefix: "valkey setnx: ",
			wantValue:  false,
		},
		{
			name: "set",
			client: &mockCmdable{setFunc: func(context.Context, string, interface{}, time.Duration) *redis.StatusCmd {
				return redis.NewStatusResult("", cause)
			}},
			call:       func(c *valkey.Cache) (any, error) { return nil, c.Set(ctx, "w11d:set", "v", time.Minute) },
			wantPrefix: "valkey set: ",
		},
		{
			name: "del",
			client: &mockCmdable{delFunc: func(context.Context, ...string) *redis.IntCmd {
				return redis.NewIntResult(0, cause)
			}},
			call:       func(c *valkey.Cache) (any, error) { return nil, c.Del(ctx, "w11d:del:a", "w11d:del:b") },
			wantPrefix: "valkey del: ",
		},
		{
			name: "del if equal",
			client: &mockCmdable{evalFunc: func(context.Context, string, []string, ...interface{}) *redis.Cmd {
				return redis.NewCmdResult(nil, cause)
			}},
			call:       func(c *valkey.Cache) (any, error) { return nil, c.DelIfEqual(ctx, "w11d:lock", "token") },
			wantPrefix: "valkey del if equal: ",
		},
		{
			name: "ping",
			client: &mockCmdable{pingFunc: func(context.Context) *redis.StatusCmd {
				return redis.NewStatusResult("", cause)
			}},
			call:       func(c *valkey.Cache) (any, error) { return nil, c.Ping(ctx) },
			wantPrefix: "valkey ping: ",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := tc.call(valkey.NewCache(tc.client))

			require.Error(t, err)
			assert.Equal(t, tc.wantPrefix+cause.Error(), err.Error())
			assert.ErrorIs(t, err, cause)
			assert.Equal(t, tc.wantValue, got)
		})
	}
}

func TestCache_Set_ClientError_StillSendsTheKeyValueAndTtlOnce(t *testing.T) {
	type setCall struct {
		key   string
		value interface{}
		ttl   time.Duration
	}
	var calls []setCall
	cache := valkey.NewCache(&mockCmdable{setFunc: func(_ context.Context, key string, value interface{}, ttl time.Duration) *redis.StatusCmd {
		calls = append(calls, setCall{key, value, ttl})
		return redis.NewStatusResult("", errors.New("connection reset by peer"))
	}})

	err := cache.Set(context.Background(), "w11d:set:key", "payload", 90*time.Second)

	require.Error(t, err)
	assert.Equal(t, []setCall{{"w11d:set:key", "payload", 90 * time.Second}}, calls)
}

func TestCache_Del_ClientError_StillSendsEveryKeyOnce(t *testing.T) {
	var calls [][]string
	cache := valkey.NewCache(&mockCmdable{delFunc: func(_ context.Context, keys ...string) *redis.IntCmd {
		calls = append(calls, keys)
		return redis.NewIntResult(0, errors.New("connection reset by peer"))
	}})

	err := cache.Del(context.Background(), "w11d:del:a", "w11d:del:b")

	require.Error(t, err)
	assert.Equal(t, [][]string{{"w11d:del:a", "w11d:del:b"}}, calls)
}
