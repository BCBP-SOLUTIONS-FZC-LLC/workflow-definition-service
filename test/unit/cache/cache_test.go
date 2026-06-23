package cache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/valkey"
)

type mockCmdable struct {
	redis.Cmdable
	getFunc   func(ctx context.Context, key string) *redis.StringCmd
	setFunc   func(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	delFunc   func(ctx context.Context, keys ...string) *redis.IntCmd
	setNXFunc func(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd
	pingFunc  func(ctx context.Context) *redis.StatusCmd
}

func (m *mockCmdable) Ping(ctx context.Context) *redis.StatusCmd {
	if m.pingFunc != nil {
		return m.pingFunc(ctx)
	}
	return nil
}

func (m *mockCmdable) Get(ctx context.Context, key string) *redis.StringCmd {
	if m.getFunc != nil {
		return m.getFunc(ctx, key)
	}
	return nil
}

func (m *mockCmdable) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
	if m.setFunc != nil {
		return m.setFunc(ctx, key, value, expiration)
	}
	return nil
}

func (m *mockCmdable) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	if m.delFunc != nil {
		return m.delFunc(ctx, keys...)
	}
	return nil
}

func (m *mockCmdable) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd {
	if m.setNXFunc != nil {
		return m.setNXFunc(ctx, key, value, expiration)
	}
	return nil
}

func TestCache_Get(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mockClient := &mockCmdable{
			getFunc: func(ctx context.Context, key string) *redis.StringCmd {
				if key != "test-key" {
					t.Errorf("unexpected key: %s", key)
				}
				return redis.NewStringResult("test-value", nil)
			},
		}
		cache := valkey.NewCache(mockClient)
		val, err := cache.Get(ctx, "test-key")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val != "test-value" {
			t.Errorf("val = %q, want test-value", val)
		}
	})

	t.Run("miss returns empty string no error", func(t *testing.T) {
		mockClient := &mockCmdable{
			getFunc: func(_ context.Context, _ string) *redis.StringCmd {
				return redis.NewStringResult("", redis.Nil)
			},
		}
		cache := valkey.NewCache(mockClient)
		val, err := cache.Get(ctx, "missing")
		if err != nil {
			t.Fatalf("cache miss should not error: %v", err)
		}
		if val != "" {
			t.Errorf("val = %q, want empty", val)
		}
	})

	t.Run("error", func(t *testing.T) {
		mockClient := &mockCmdable{
			getFunc: func(ctx context.Context, key string) *redis.StringCmd {
				return redis.NewStringResult("", errors.New("redis error"))
			},
		}
		cache := valkey.NewCache(mockClient)
		_, err := cache.Get(ctx, "test-key")
		if err == nil {
			t.Fatal("expected error but got nil")
		}
	})
}

func TestCache_Set(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mockClient := &mockCmdable{
			setFunc: func(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
				if key != "test-key" || value != "test-value" || expiration != 5*time.Minute {
					t.Errorf("unexpected args: %s, %v, %v", key, value, expiration)
				}
				return redis.NewStatusResult("OK", nil)
			},
		}
		cache := valkey.NewCache(mockClient)
		if err := cache.Set(ctx, "test-key", "test-value", 5*time.Minute); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("error", func(t *testing.T) {
		mockClient := &mockCmdable{
			setFunc: func(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
				return redis.NewStatusResult("", errors.New("redis error"))
			},
		}
		cache := valkey.NewCache(mockClient)
		if err := cache.Set(ctx, "test-key", "test-value", 5*time.Minute); err == nil {
			t.Fatal("expected error but got nil")
		}
	})
}

func TestCache_Ping(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mockClient := &mockCmdable{
			pingFunc: func(ctx context.Context) *redis.StatusCmd {
				return redis.NewStatusResult("PONG", nil)
			},
		}
		cache := valkey.NewCache(mockClient)
		if err := cache.Ping(ctx); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("error", func(t *testing.T) {
		mockClient := &mockCmdable{
			pingFunc: func(ctx context.Context) *redis.StatusCmd {
				return redis.NewStatusResult("", errors.New("redis down"))
			},
		}
		cache := valkey.NewCache(mockClient)
		if err := cache.Ping(ctx); err == nil {
			t.Fatal("expected error but got nil")
		}
	})
}

func TestCache_Del(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mockClient := &mockCmdable{
			delFunc: func(ctx context.Context, keys ...string) *redis.IntCmd {
				if len(keys) != 2 || keys[0] != "k1" || keys[1] != "k2" {
					t.Errorf("unexpected keys: %v", keys)
				}
				return redis.NewIntResult(2, nil)
			},
		}
		cache := valkey.NewCache(mockClient)
		if err := cache.Del(ctx, "k1", "k2"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("error", func(t *testing.T) {
		mockClient := &mockCmdable{
			delFunc: func(ctx context.Context, keys ...string) *redis.IntCmd {
				return redis.NewIntResult(0, errors.New("redis error"))
			},
		}
		cache := valkey.NewCache(mockClient)
		if err := cache.Del(ctx, "k1"); err == nil {
			t.Fatal("expected error but got nil")
		}
	})
}

func TestCache_SetNX(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		result  bool
		wantSet bool
	}{
		{"key absent — set succeeds", true, true},
		{"key present — set rejected", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := tt.result
			mockClient := &mockCmdable{
				setNXFunc: func(_ context.Context, _ string, _ interface{}, _ time.Duration) *redis.BoolCmd {
					return redis.NewBoolResult(r, nil)
				},
			}
			cache := valkey.NewCache(mockClient)
			ok, err := cache.SetNX(ctx, "nx-k", "new", time.Minute)
			if err != nil {
				t.Fatalf("SetNX: %v", err)
			}
			if ok != tt.wantSet {
				t.Errorf("SetNX = %v, want %v", ok, tt.wantSet)
			}
		})
	}

	t.Run("error", func(t *testing.T) {
		mockClient := &mockCmdable{
			setNXFunc: func(_ context.Context, _ string, _ interface{}, _ time.Duration) *redis.BoolCmd {
				return redis.NewBoolResult(false, errors.New("redis error"))
			},
		}
		cache := valkey.NewCache(mockClient)
		if _, err := cache.SetNX(ctx, "k", "v", time.Minute); err == nil {
			t.Fatal("expected error but got nil")
		}
	})
}
