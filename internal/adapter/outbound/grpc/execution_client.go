package grpc

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	executionv1 "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/gen/proto/execution/v1"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

// maxCallAttempts bounds transient retries (1 initial try + retries) on the
// archive guard call; only codes.Unavailable / DeadlineExceeded are retried.
const maxCallAttempts = 3

var _ port.ExecutionService = (*ExecutionClient)(nil)

type ExecutionClient struct {
	client      executionv1.ExecutionServiceClient
	conn        *grpc.ClientConn
	callTimeout time.Duration
}

func NewExecutionClient(addr string, callTimeout time.Duration) (*ExecutionClient, error) {
	// Insecure credentials are intentional: intra-cluster traffic only; mTLS is terminated at the service mesh.
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial execution service: %w", err)
	}
	return &ExecutionClient{
		client:      executionv1.NewExecutionServiceClient(conn),
		conn:        conn,
		callTimeout: callTimeout,
	}, nil
}

func (c *ExecutionClient) Close() error {
	if err := c.conn.Close(); err != nil {
		return fmt.Errorf("close execution client: %w", err)
	}
	return nil
}

func (c *ExecutionClient) CheckActiveInstances(
	ctx context.Context,
	tenantID, workflowID uuid.UUID,
) (hasActive bool, count int32, err error) {
	req := &executionv1.CheckActiveInstancesRequest{
		TenantId:   tenantID.String(),
		WorkflowId: workflowID.String(),
	}
	var lastErr error
	for attempt := 1; attempt <= maxCallAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, c.callTimeout)
		resp, callErr := c.client.CheckActiveInstances(attemptCtx, req)
		cancel()
		if callErr == nil {
			return resp.GetHasActive(), resp.GetCount(), nil
		}
		lastErr = callErr
		if attempt == maxCallAttempts || !isRetryableGRPC(callErr) {
			break
		}
		select {
		case <-ctx.Done():
			return false, 0, fmt.Errorf("%w: check active instances: %w", domain.ErrUpstreamUnavailable, ctx.Err())
		case <-time.After(backoff(attempt)):
		}
	}
	return false, 0, fmt.Errorf("%w: check active instances: %w", domain.ErrUpstreamUnavailable, lastErr)
}

func (c *ExecutionClient) PauseUserTasks(ctx context.Context, tenantID, userID uuid.UUID) error {
	req := &executionv1.PauseUserTasksRequest{
		TenantId: tenantID.String(),
		UserId:   userID.String(),
	}
	var lastErr error
	for attempt := 1; attempt <= maxCallAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, c.callTimeout)
		_, callErr := c.client.PauseUserTasks(attemptCtx, req)
		cancel()
		if callErr == nil {
			return nil
		}
		lastErr = callErr
		if attempt == maxCallAttempts || !isRetryableGRPC(callErr) {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: pause user tasks: %w", domain.ErrUpstreamUnavailable, ctx.Err())
		case <-time.After(backoff(attempt)):
		}
	}
	return fmt.Errorf("%w: pause user tasks: %w", domain.ErrUpstreamUnavailable, lastErr)
}

func isRetryableGRPC(err error) bool {
	c := status.Code(err)
	return c == codes.Unavailable || c == codes.DeadlineExceeded
}

func backoff(attempt int) time.Duration {
	d := 50 * time.Millisecond << (attempt - 1)
	if d > 500*time.Millisecond {
		d = 500 * time.Millisecond
	}
	return d
}
