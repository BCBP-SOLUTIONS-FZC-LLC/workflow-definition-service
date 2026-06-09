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
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	executionv1 "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/gen/proto/execution/v1"
	grpcadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/grpc"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const defaultTimeout = 5 * time.Second

// fakeExecutionServer implements executionv1.ExecutionServiceServer.
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

// startServer starts a local gRPC server and returns its address plus a cleanup func.
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

func TestExecutionClient_CheckActiveInstances_HasActive(t *testing.T) {
	fake := &fakeExecutionServer{
		resp: &executionv1.CheckActiveInstancesResponse{HasActive: true, Count: 3},
	}
	addr := startServer(t, fake)
	c, err := grpcadapter.NewExecutionClient(addr, defaultTimeout)
	if err != nil {
		t.Fatalf("NewExecutionClient: %v", err)
	}
	defer c.Close() //nolint:errcheck

	ctx := context.Background()
	// The client dials lazily; wait for the connection to come up.
	conn, dialErr := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if dialErr != nil {
		t.Fatalf("wait dial: %v", dialErr)
	}
	conn.Close() //nolint:errcheck

	hasActive, count, err := c.CheckActiveInstances(ctx, uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("CheckActiveInstances: %v", err)
	}
	if !hasActive {
		t.Error("expected hasActive=true")
	}
	if count != 3 {
		t.Errorf("expected count=3, got %d", count)
	}
}

func TestExecutionClient_CheckActiveInstances_NoActive(t *testing.T) {
	fake := &fakeExecutionServer{
		resp: &executionv1.CheckActiveInstancesResponse{HasActive: false, Count: 0},
	}
	addr := startServer(t, fake)
	c, err := grpcadapter.NewExecutionClient(addr, defaultTimeout)
	if err != nil {
		t.Fatalf("NewExecutionClient: %v", err)
	}
	defer c.Close() //nolint:errcheck

	hasActive, count, err := c.CheckActiveInstances(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hasActive {
		t.Error("expected hasActive=false")
	}
	if count != 0 {
		t.Errorf("expected count=0, got %d", count)
	}
}

func TestExecutionClient_CheckActiveInstances_UpstreamError(t *testing.T) {
	fake := &fakeExecutionServer{
		err: status.Error(codes.Internal, "internal error"),
	}
	addr := startServer(t, fake)
	c, err := grpcadapter.NewExecutionClient(addr, defaultTimeout)
	if err != nil {
		t.Fatalf("NewExecutionClient: %v", err)
	}
	defer c.Close() //nolint:errcheck

	_, _, err = c.CheckActiveInstances(context.Background(), uuid.New(), uuid.New())
	if err == nil {
		t.Fatal("expected error from upstream")
	}
	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Errorf("expected ErrUpstreamUnavailable, got %v", err)
	}
}

// slowExecutionServer blocks until the context deadline fires, simulating a hung upstream.
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

func TestExecutionClient_CheckActiveInstances_CallTimeout(t *testing.T) {
	addr := startServer(t, &slowExecutionServer{})

	// 50 ms timeout — the slow server blocks indefinitely so this must fire.
	c, err := grpcadapter.NewExecutionClient(addr, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("NewExecutionClient: %v", err)
	}
	defer c.Close() //nolint:errcheck

	start := time.Now()
	_, _, err = c.CheckActiveInstances(context.Background(), uuid.New(), uuid.New())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from timeout")
	}
	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Errorf("expected ErrUpstreamUnavailable, got %v", err)
	}
	// Should complete well within 1 s; the 50 ms deadline must not be silently ignored.
	if elapsed > time.Second {
		t.Errorf("call took %v; deadline appears to have been ignored", elapsed)
	}
}
