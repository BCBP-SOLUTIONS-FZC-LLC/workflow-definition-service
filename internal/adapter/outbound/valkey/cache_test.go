package valkey

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type mockCmdable struct {
	redis.Cmdable
	getFunc   func(ctx context.Context, key string) *redis.StringCmd
	setFunc   func(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	delFunc   func(ctx context.Context, keys ...string) *redis.IntCmd
	setNXFunc func(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd
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

		cache := NewCache(mockClient)
		val, err := cache.Get(ctx, "test-key")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val != "test-value" {
			t.Errorf("val = %q, want test-value", val)
		}
	})

	t.Run("error", func(t *testing.T) {
		mockClient := &mockCmdable{
			getFunc: func(ctx context.Context, key string) *redis.StringCmd {
				return redis.NewStringResult("", errors.New("redis error"))
			},
		}

		cache := NewCache(mockClient)
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

		cache := NewCache(mockClient)
		err := cache.Set(ctx, "test-key", "test-value", 5*time.Minute)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("error", func(t *testing.T) {
		mockClient := &mockCmdable{
			setFunc: func(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
				return redis.NewStatusResult("", errors.New("redis error"))
			},
		}

		cache := NewCache(mockClient)
		err := cache.Set(ctx, "test-key", "test-value", 5*time.Minute)
		if err == nil {
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

		cache := NewCache(mockClient)
		err := cache.Del(ctx, "k1", "k2")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("error", func(t *testing.T) {
		mockClient := &mockCmdable{
			delFunc: func(ctx context.Context, keys ...string) *redis.IntCmd {
				return redis.NewIntResult(0, errors.New("redis error"))
			},
		}

		cache := NewCache(mockClient)
		err := cache.Del(ctx, "k1")
		if err == nil {
			t.Fatal("expected error but got nil")
		}
	})
}

func TestCache_SetNX(t *testing.T) {
	ctx := context.Background()

	t.Run("success true", func(t *testing.T) {
		mockClient := &mockCmdable{
			setNXFunc: func(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd {
				return redis.NewBoolResult(true, nil)
			},
		}

		cache := NewCache(mockClient)
		ok, err := cache.SetNX(ctx, "test-key", "test-value", 5*time.Minute)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok {
			t.Error("expected true")
		}
	})

	t.Run("success false", func(t *testing.T) {
		mockClient := &mockCmdable{
			setNXFunc: func(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd {
				return redis.NewBoolResult(false, nil)
			},
		}

		cache := NewCache(mockClient)
		ok, err := cache.SetNX(ctx, "test-key", "test-value", 5*time.Minute)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok {
			t.Error("expected false")
		}
	})

	t.Run("error", func(t *testing.T) {
		mockClient := &mockCmdable{
			setNXFunc: func(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd {
				return redis.NewBoolResult(false, errors.New("redis error"))
			},
		}

		cache := NewCache(mockClient)
		_, err := cache.SetNX(ctx, "test-key", "test-value", 5*time.Minute)
		if err == nil {
			t.Fatal("expected error but got nil")
		}
	})
}
