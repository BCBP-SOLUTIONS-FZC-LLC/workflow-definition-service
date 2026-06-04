package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	definitionv1 "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/gen/proto/definition/v1"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type Server struct {
	definitionv1.UnimplementedDefinitionServiceServer
	log port.Logger
}

func NewServer(log port.Logger) *Server {
	return &Server{log: log}
}

func (s *Server) GetCompiledWorkflow(
	_ context.Context,
	_ *definitionv1.GetCompiledWorkflowRequest,
) (*definitionv1.GetCompiledWorkflowResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not yet implemented") //nolint:wrapcheck
}
