package grpc

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	executionv1 "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/gen/proto/execution/v1"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.ExecutionService = (*ExecutionClient)(nil)

type ExecutionClient struct {
	client executionv1.ExecutionServiceClient
	conn   *grpc.ClientConn
}

func NewExecutionClient(addr string) (*ExecutionClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial execution service: %w", err)
	}
	return &ExecutionClient{
		client: executionv1.NewExecutionServiceClient(conn),
		conn:   conn,
	}, nil
}

func (c *ExecutionClient) Close() error {
	if err := c.conn.Close(); err != nil {
		return fmt.Errorf("close execution client: %w", err)
	}
	return nil
}

// CheckActiveInstances returns whether the workflow has any running or paused instances.
func (c *ExecutionClient) CheckActiveInstances(
	ctx context.Context,
	tenantID, workflowID uuid.UUID,
) (hasActive bool, count int32, err error) {
	resp, err := c.client.CheckActiveInstances(ctx, &executionv1.CheckActiveInstancesRequest{
		TenantId:   tenantID.String(),
		WorkflowId: workflowID.String(),
	})
	if err != nil {
		return false, 0, fmt.Errorf("%w: check active instances: %w", domain.ErrUpstreamUnavailable, err)
	}
	return resp.GetHasActive(), resp.GetCount(), nil
}
