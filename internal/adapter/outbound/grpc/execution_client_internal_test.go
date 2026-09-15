package grpc

import (
	"testing"
	"time"
)

func TestNewExecutionClient_DialError(t *testing.T) {
	// A control character in the target fails resolver URL parsing inside
	// grpc.NewClient, unlike a merely-unreachable address (which dials lazily
	// and only errors on first RPC).
	if _, err := NewExecutionClient("unix:\x00bad", time.Second); err == nil {
		t.Fatal("expected error for malformed target address")
	}
}

func TestExecutionClient_Close_Error(t *testing.T) {
	c, err := NewExecutionClient("127.0.0.1:0", time.Second)
	if err != nil {
		t.Fatalf("NewExecutionClient: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err == nil {
		t.Fatal("expected error on second Close (already closed)")
	}
}

func TestBackoff_CapsAt500ms(t *testing.T) {
	if got := backoff(10); got != 500*time.Millisecond {
		t.Errorf("backoff(10) = %v, want 500ms cap", got)
	}
}
