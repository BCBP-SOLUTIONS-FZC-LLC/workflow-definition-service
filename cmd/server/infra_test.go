package main

import (
	"context"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

func TestNewCacheStore_ADeadCacheDoesNotStopStartup(t *testing.T) {
	cfg := &config.Config{
		ValkeyAddr:         "127.0.0.1:1",
		ValkeyDialTimeout:  200 * time.Millisecond,
		ValkeyReadTimeout:  200 * time.Millisecond,
		ValkeyWriteTimeout: 200 * time.Millisecond,
	}

	store, client := newCacheStore(context.Background(), cfg, nopLogger{})
	t.Cleanup(func() { _ = client.Close() })
	if store == nil {
		t.Fatal("no cache store")
	}
}

type nopLogger struct{}

func (nopLogger) Debug(string, map[string]any) {}
func (nopLogger) Info(string, map[string]any)  {}
func (nopLogger) Warn(string, map[string]any)  {}
func (nopLogger) Error(string, map[string]any) {}
func (nopLogger) Fatal(string, map[string]any) {}
