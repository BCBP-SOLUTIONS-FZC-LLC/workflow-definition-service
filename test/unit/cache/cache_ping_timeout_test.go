package cache_test

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/valkey"
)

func startPingBlackHole(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return ln.Addr().String()
}

func TestCache_Ping_ServerNeverAnswers_ReturnsWrappedTimeout(t *testing.T) {
	cases := []struct {
		name string
		opts func(addr string) *redis.Options
	}{
		{
			name: "read timeout elapses",
			opts: func(addr string) *redis.Options {
				return &redis.Options{
					Addr:         addr,
					DialTimeout:  time.Second,
					ReadTimeout:  100 * time.Millisecond,
					WriteTimeout: 100 * time.Millisecond,
					MaxRetries:   -1,
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := redis.NewClient(tc.opts(startPingBlackHole(t)))
			t.Cleanup(func() { _ = client.Close() })
			cache := valkey.NewCache(client)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err := cache.Ping(ctx)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "valkey ping: ")
			var netErr net.Error
			require.True(t, errors.As(err, &netErr), "cause %v must be a network error", err)
			assert.True(t, netErr.Timeout(), "cause %v must be a timeout", err)
			assert.NoError(t, ctx.Err(), "the client timeout must fire before the caller's deadline")
		})
	}
}
