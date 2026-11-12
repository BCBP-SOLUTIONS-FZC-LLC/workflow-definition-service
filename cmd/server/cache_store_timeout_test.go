package main

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

func TestValkeyOptions_TheMainClientKeepsTheDefaultRetriesAndTheReadinessClientIsOneAttempt(t *testing.T) {
	cfg := &config.Config{
		ValkeyAddr: "valkey:6379", ValkeyPassword: "pw",
		ValkeyDialTimeout: 2 * time.Second, ValkeyReadTimeout: time.Second, ValkeyWriteTimeout: 3 * time.Second,
	}

	main := newValkeyOptions(cfg)
	ready := newReadinessOptions(cfg)

	assert.Zero(t, main.MaxRetries, "zero selects the go-redis default of three retries after a failover")
	assert.Equal(t, -1, ready.MaxRetries, "the default of three retries would multiply the dial timeout past the readiness probe's timeout")
	for _, o := range []struct{ addr, pw string }{{main.Addr, main.Password}, {ready.Addr, ready.Password}} {
		assert.Equal(t, "valkey:6379", o.addr)
		assert.Equal(t, "pw", o.pw)
	}
	assert.Equal(t, 2*time.Second, ready.DialTimeout)
	assert.Equal(t, time.Second, ready.ReadTimeout)
	assert.Equal(t, 3*time.Second, ready.WriteTimeout)
	assert.Equal(t, ready.DialTimeout, main.DialTimeout)
}

func TestNewCacheStore_PingIsOneAttemptWhileAStoreCommandRetries(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	var accepted atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			_ = conn.Close() // a dropped connection is what a failover looks like to the client
		}
	}()
	cfg := &config.Config{
		ValkeyAddr:         ln.Addr().String(),
		ValkeyDialTimeout:  time.Second,
		ValkeyReadTimeout:  50 * time.Millisecond,
		ValkeyWriteTimeout: 50 * time.Millisecond,
	}
	store, ready, closer := newCacheStore(context.Background(), cfg, nopLogger{})
	t.Cleanup(func() { _ = closer.Close() })
	atStartup := accepted.Load()
	assert.Equal(t, int32(1), atStartup, "the startup ping is one attempt")

	require.Error(t, ready.Ping(context.Background()))
	assert.Equal(t, atStartup+1, accepted.Load(), "a readiness ping is one attempt")

	before := accepted.Load()
	_, err = store.Get(context.Background(), "k")
	require.Error(t, err)
	assert.Equal(t, before+4, accepted.Load(), "a store command is tried once and retried three times by the client")
}
