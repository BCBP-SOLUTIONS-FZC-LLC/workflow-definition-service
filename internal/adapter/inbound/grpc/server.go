package grpc

import (
	"context"
	"errors"

	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	definitionv1 "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/gen/proto/definition/v1"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type Server struct {
	definitionv1.UnimplementedDefinitionServiceServer
	log      port.Logger
	versions port.WorkflowVersionRepository
}

func NewServer(log port.Logger, versions port.WorkflowVersionRepository) *Server {
	return &Server{log: log, versions: versions}
}

func (s *Server) GetCompiledWorkflow(
	ctx context.Context,
	req *definitionv1.GetCompiledWorkflowRequest,
) (*definitionv1.GetCompiledWorkflowResponse, error) {
	tenantID, err := uuid.Parse(req.TenantId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid tenant_id") //nolint:wrapcheck
	}
	versionID, err := uuid.Parse(req.WorkflowVersionId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid workflow_version_id") //nolint:wrapcheck
	}

	ctx = pgcommon.WithGUCSet(ctx, pgdomain.GUCSet{TenantID: req.TenantId})

	v, err := s.versions.GetByID(ctx, tenantID, versionID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "workflow version not found") //nolint:wrapcheck
		}
		s.log.Error("GetCompiledWorkflow: repo error", map[string]any{"error": err.Error()})
		return nil, status.Error(codes.Internal, "internal error") //nolint:wrapcheck
	}

	var compiledPlanJSON string
	if v.CompiledPlanJSON != nil {
		compiledPlanJSON = *v.CompiledPlanJSON
	}
	var versionNumber int32
	if v.VersionNumber != nil {
		versionNumber = *v.VersionNumber
	}

	return &definitionv1.GetCompiledWorkflowResponse{
		WorkflowId:       v.WorkflowID.String(),
		VersionId:        v.ID.String(),
		VersionNumber:    versionNumber,
		Status:           string(v.Status),
		IsValid:          v.IsValid,
		CompiledPlanJson: compiledPlanJSON,
	}, nil
}
