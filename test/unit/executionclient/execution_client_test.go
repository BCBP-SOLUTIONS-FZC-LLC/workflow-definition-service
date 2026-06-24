package executionclient_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	executionv1 "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/gen/proto/execution/v1"
	grpcadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/grpc"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const defaultTimeout = 5 * time.Second

type fakeExecutionServer struct {
	executionv1.UnimplementedExecutionServiceServer
	resp *executionv1.CheckActiveInstancesResponse
	err  error
}

func (f *fakeExecutionServer) CheckActiveInstances(
	_ context.Context,
	_ *executionv1.CheckActiveInstancesRequest,
) (*executionv1.CheckActiveInstancesResponse, error) {
	return f.resp, f.err
}

type flakyExecutionServer struct {
	executionv1.UnimplementedExecutionServiceServer
	failCount int
	calls     int
	resp      *executionv1.CheckActiveInstancesResponse
}

func (f *flakyExecutionServer) CheckActiveInstances(
	_ context.Context,
	_ *executionv1.CheckActiveInstancesRequest,
) (*executionv1.CheckActiveInstancesResponse, error) {
	f.calls++
	if f.calls <= f.failCount {
		return nil, status.Error(codes.Unavailable, "transient")
	}
	return f.resp, nil
}

type slowExecutionServer struct {
	executionv1.UnimplementedExecutionServiceServer
}

func (s *slowExecutionServer) CheckActiveInstances(
	ctx context.Context,
	_ *executionv1.CheckActiveInstancesRequest,
) (*executionv1.CheckActiveInstancesResponse, error) {
	<-ctx.Done()
	return nil, status.FromContextError(ctx.Err()).Err()
}

// verify that non-retryable errors do not trigger retries.
type countingServer struct {
	executionv1.UnimplementedExecutionServiceServer
	calls int
	code  codes.Code
}

func (s *countingServer) CheckActiveInstances(
	_ context.Context,
	_ *executionv1.CheckActiveInstancesRequest,
) (*executionv1.CheckActiveInstancesResponse, error) {
	s.calls++
	return nil, status.Error(s.code, "error")
}

func (s *countingServer) PauseUserTasks(
	_ context.Context,
	_ *executionv1.PauseUserTasksRequest,
) (*executionv1.PauseUserTasksResponse, error) {
	s.calls++
	return nil, status.Error(s.code, "error")
}

type pauseServer struct {
	executionv1.UnimplementedExecutionServiceServer
	err error
}

func (p *pauseServer) PauseUserTasks(
	_ context.Context,
	_ *executionv1.PauseUserTasksRequest,
) (*executionv1.PauseUserTasksResponse, error) {
	if p.err != nil {
		return nil, p.err
	}
	return &executionv1.PauseUserTasksResponse{}, nil
}

type flakyPauseServer struct {
	executionv1.UnimplementedExecutionServiceServer
	failCount int
	calls     int
}

func (f *flakyPauseServer) PauseUserTasks(
	_ context.Context,
	_ *executionv1.PauseUserTasksRequest,
) (*executionv1.PauseUserTasksResponse, error) {
	f.calls++
	if f.calls <= f.failCount {
		return nil, status.Error(codes.Unavailable, "transient")
	}
	return &executionv1.PauseUserTasksResponse{}, nil
}

func startServer(t *testing.T, srv executionv1.ExecutionServiceServer) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := grpc.NewServer()
	executionv1.RegisterExecutionServiceServer(s, srv)
	go s.Serve(lis) //nolint:errcheck
	t.Cleanup(func() { s.Stop() })
	return lis.Addr().String()
}

func TestExecutionClient_New_AndClose(t *testing.T) {
	addr := startServer(t, &fakeExecutionServer{})
	c, err := grpcadapter.NewExecutionClient(addr, defaultTimeout)
	if err != nil {
		t.Fatalf("NewExecutionClient: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestExecutionClient_CheckActiveInstances(t *testing.T) {
	tests := []struct {
		name          string
		srv           executionv1.ExecutionServiceServer
		clientTimeout time.Duration
		makeCtx       func(t *testing.T) context.Context
		wantErr       bool
		wantSentinel  error
		wantActive    bool
		wantCount     int32
		checkServer   func(t *testing.T, srv executionv1.ExecutionServiceServer)
		checkElapsed  func(t *testing.T, elapsed time.Duration)
	}{
		{
			name:       "active instances",
			srv:        &fakeExecutionServer{resp: &executionv1.CheckActiveInstancesResponse{HasActive: true, Count: 3}},
			wantActive: true,
			wantCount:  3,
		},
		{
			name: "no active instances",
			srv:  &fakeExecutionServer{resp: &executionv1.CheckActiveInstancesResponse{}},
		},
		{
			name:         "upstream error",
			srv:          &fakeExecutionServer{err: status.Error(codes.Internal, "internal error")},
			wantErr:      true,
			wantSentinel: domain.ErrUpstreamUnavailable,
		},
		{
			name:          "call timeout",
			srv:           &slowExecutionServer{},
			clientTimeout: 50 * time.Millisecond,
			wantErr:       true,
			wantSentinel:  domain.ErrUpstreamUnavailable,
			checkElapsed: func(t *testing.T, elapsed time.Duration) {
				if elapsed > time.Second {
					t.Errorf("call took %v; deadline appears to have been ignored", elapsed)
				}
			},
		},
		{
			name: "retries then succeeds",
			srv: &flakyExecutionServer{
				failCount: 2,
				resp:      &executionv1.CheckActiveInstancesResponse{HasActive: true, Count: 1},
			},
			wantActive: true,
			wantCount:  1,
			checkServer: func(t *testing.T, srv executionv1.ExecutionServiceServer) {
				if fake := srv.(*flakyExecutionServer); fake.calls != 3 {
					t.Errorf("expected 3 attempts (2 retries), got %d", fake.calls)
				}
			},
		},
		{
			name:         "non-retryable error makes only 1 attempt",
			srv:          &countingServer{code: codes.PermissionDenied},
			wantErr:      true,
			wantSentinel: domain.ErrUpstreamUnavailable,
			checkServer: func(t *testing.T, srv executionv1.ExecutionServiceServer) {
				if got := srv.(*countingServer).calls; got != 1 {
					t.Errorf("expected 1 attempt for non-retryable error, got %d", got)
				}
			},
		},
		{
			name: "context cancelled during backoff short-circuits retry",
			srv:  &countingServer{code: codes.Unavailable},
			makeCtx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				time.AfterFunc(30*time.Millisecond, cancel)
				return ctx
			},
			wantErr:      true,
			wantSentinel: domain.ErrUpstreamUnavailable,
			checkElapsed: func(t *testing.T, elapsed time.Duration) {
				if elapsed > 100*time.Millisecond {
					t.Errorf("expected context cancel to short-circuit retry; elapsed = %v", elapsed)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := startServer(t, tt.srv)
			timeout := defaultTimeout
			if tt.clientTimeout > 0 {
				timeout = tt.clientTimeout
			}
			c, err := grpcadapter.NewExecutionClient(addr, timeout)
			if err != nil {
				t.Fatalf("NewExecutionClient: %v", err)
			}
			defer c.Close() //nolint:errcheck

			ctx := context.Background()
			if tt.makeCtx != nil {
				ctx = tt.makeCtx(t)
			}
			start := time.Now()
			hasActive, count, err := c.CheckActiveInstances(ctx, uuid.New(), uuid.New())
			elapsed := time.Since(start)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantSentinel != nil && !errors.Is(err, tt.wantSentinel) {
					t.Errorf("expected %v, got %v", tt.wantSentinel, err)
				}
				if tt.checkElapsed != nil {
					tt.checkElapsed(t, elapsed)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if hasActive != tt.wantActive {
				t.Errorf("hasActive = %v, want %v", hasActive, tt.wantActive)
			}
			if count != tt.wantCount {
				t.Errorf("count = %d, want %d", count, tt.wantCount)
			}
			if tt.checkServer != nil {
				tt.checkServer(t, tt.srv)
			}
		})
	}
}

func TestExecutionClient_PauseUserTasks(t *testing.T) {
	tests := []struct {
		name         string
		srv          executionv1.ExecutionServiceServer
		makeCtx      func(t *testing.T) context.Context
		wantErr      bool
		wantSentinel error
		checkServer  func(t *testing.T, srv executionv1.ExecutionServiceServer)
		checkElapsed func(t *testing.T, elapsed time.Duration)
	}{
		{
			name: "ok",
			srv:  &pauseServer{},
		},
		{
			name:         "upstream error",
			srv:          &pauseServer{err: status.Error(codes.Internal, "internal error")},
			wantErr:      true,
			wantSentinel: domain.ErrUpstreamUnavailable,
		},
		{
			name: "retries then succeeds",
			srv:  &flakyPauseServer{failCount: 2},
			checkServer: func(t *testing.T, srv executionv1.ExecutionServiceServer) {
				if fake := srv.(*flakyPauseServer); fake.calls != 3 {
					t.Errorf("expected 3 attempts (2 retries), got %d", fake.calls)
				}
			},
		},
		{
			name:         "non-retryable error makes only 1 attempt",
			srv:          &countingServer{code: codes.PermissionDenied},
			wantErr:      true,
			wantSentinel: domain.ErrUpstreamUnavailable,
			checkServer: func(t *testing.T, srv executionv1.ExecutionServiceServer) {
				if got := srv.(*countingServer).calls; got != 1 {
					t.Errorf("expected 1 attempt for non-retryable error, got %d", got)
				}
			},
		},
		{
			name: "context cancelled during backoff short-circuits retry",
			srv:  &countingServer{code: codes.Unavailable},
			makeCtx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				time.AfterFunc(30*time.Millisecond, cancel)
				return ctx
			},
			wantErr:      true,
			wantSentinel: domain.ErrUpstreamUnavailable,
			checkElapsed: func(t *testing.T, elapsed time.Duration) {
				if elapsed > 100*time.Millisecond {
					t.Errorf("expected context cancel to short-circuit retry; elapsed = %v", elapsed)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := startServer(t, tt.srv)
			c, err := grpcadapter.NewExecutionClient(addr, defaultTimeout)
			if err != nil {
				t.Fatalf("NewExecutionClient: %v", err)
			}
			defer c.Close() //nolint:errcheck

			ctx := context.Background()
			if tt.makeCtx != nil {
				ctx = tt.makeCtx(t)
			}
			start := time.Now()
			err = c.PauseUserTasks(ctx, uuid.New(), uuid.New())
			elapsed := time.Since(start)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantSentinel != nil && !errors.Is(err, tt.wantSentinel) {
					t.Errorf("expected %v, got %v", tt.wantSentinel, err)
				}
				if tt.checkElapsed != nil {
					tt.checkElapsed(t, elapsed)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.checkServer != nil {
				tt.checkServer(t, tt.srv)
			}
		})
	}
}
