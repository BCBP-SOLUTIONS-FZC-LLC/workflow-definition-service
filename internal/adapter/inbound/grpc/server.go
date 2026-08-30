package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	definitionv1 "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/gen/proto/definition/v1"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

type Server struct {
	definitionv1.UnimplementedDefinitionServiceServer
	log      port.Logger
	versions port.WorkflowVersionRepository
	cache    port.CacheStore // optional; nil disables the compiled-plan cache
	cacheTTL time.Duration
}

func NewServer(log port.Logger, versions port.WorkflowVersionRepository, cache port.CacheStore, cacheTTL time.Duration) *Server {
	return &Server{log: log, versions: versions, cache: cache, cacheTTL: cacheTTL}
}

type cachedPlan struct {
	WorkflowID       string `json:"workflow_id"`
	VersionID        string `json:"version_id"`
	VersionNumber    int32  `json:"version_number"`
	Status           string `json:"status"`
	IsValid          bool   `json:"is_valid"`
	CompiledPlanJSON string `json:"compiled_plan_json"`
	SchemaVersion    int32  `json:"dsl_schema_version"`
}

func (cp cachedPlan) toResponse() *definitionv1.GetCompiledWorkflowResponse {
	return &definitionv1.GetCompiledWorkflowResponse{
		WorkflowId:       cp.WorkflowID,
		VersionId:        cp.VersionID,
		VersionNumber:    cp.VersionNumber,
		Status:           cp.Status,
		IsValid:          cp.IsValid,
		CompiledPlanJson: cp.CompiledPlanJSON,
		DslSchemaVersion: cp.SchemaVersion,
	}
}

// compiledPlanSchemaVersion pulls the DSL schema major version off an
// already-fetched compiled_plan_json blob (dsl.CompiledCollaboration.SchemaVersion)
// without importing the full dsl package for one field.
func compiledPlanSchemaVersion(compiledPlanJSON string) int32 {
	var v struct {
		SchemaVersion int32 `json:"schema_version"`
	}
	_ = json.Unmarshal([]byte(compiledPlanJSON), &v)
	return v.SchemaVersion
}

func (s *Server) GetCompiledWorkflow(
	ctx context.Context,
	req *definitionv1.GetCompiledWorkflowRequest,
) (*definitionv1.GetCompiledWorkflowResponse, error) {
	if req.TenantId == "" {
		return nil, status.Error(codes.PermissionDenied, "tenant_id is required") //nolint:wrapcheck
	}
	if req.WorkflowVersionId == "" {
		return nil, status.Error(codes.InvalidArgument, "workflow_version_id is required") //nolint:wrapcheck
	}
	tenantID, err := uuid.Parse(req.TenantId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid tenant_id") //nolint:wrapcheck
	}
	versionID, err := uuid.Parse(req.WorkflowVersionId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid workflow_version_id") //nolint:wrapcheck
	}

	cacheKey := domain.CompiledPlanCacheKey(tenantID, versionID)
	if resp, ok := s.readCache(ctx, cacheKey); ok {
		observability.IncCounter(observability.WFCacheHitsTotal)
		return resp, nil
	}
	observability.IncCounter(observability.WFCacheMissesTotal)

	ctx = pgcommon.WithGUCSet(ctx, pgdomain.GUCSet{TenantID: req.TenantId})

	v, err := s.versions.GetByID(ctx, tenantID, versionID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "workflow version not found") //nolint:wrapcheck
		}
		s.log.Error("GetCompiledWorkflow: repo error", map[string]any{"error": err.Error()})
		return nil, status.Error(codes.Internal, "internal error") //nolint:wrapcheck
	}

	cp := cachedPlan{
		WorkflowID: v.WorkflowID.String(),
		VersionID:  v.ID.String(),
		Status:     string(v.Status),
		IsValid:    v.IsValid,
	}
	if v.CompiledPlanJSON != nil {
		cp.CompiledPlanJSON = *v.CompiledPlanJSON
		cp.SchemaVersion = compiledPlanSchemaVersion(*v.CompiledPlanJSON)
	}
	if v.VersionNumber != nil {
		cp.VersionNumber = *v.VersionNumber
	}

	s.writeCache(ctx, cacheKey, cp)
	return cp.toResponse(), nil
}

// readCache returns the cached response on a hit. It fails open: any miss or
// cache/unmarshal error returns ok=false so the caller falls through to the DB.
func (s *Server) readCache(ctx context.Context, key string) (*definitionv1.GetCompiledWorkflowResponse, bool) {
	if s.cache == nil {
		return nil, false
	}
	raw, err := s.cache.Get(ctx, key)
	if err != nil {
		s.log.Warn("compiled-plan cache read failed", map[string]any{"key": key, "error": err.Error()})
		return nil, false
	}
	if raw == "" {
		return nil, false
	}
	var cp cachedPlan
	if err := json.Unmarshal([]byte(raw), &cp); err != nil {
		s.log.Warn("compiled-plan cache decode failed", map[string]any{"key": key, "error": err.Error()})
		return nil, false
	}
	return cp.toResponse(), true
}

func (s *Server) writeCache(ctx context.Context, key string, cp cachedPlan) {
	if s.cache == nil {
		return
	}
	raw, err := json.Marshal(cp)
	if err != nil {
		return
	}
	if err := s.cache.Set(ctx, key, string(raw), s.cacheTTL); err != nil {
		s.log.Warn("compiled-plan cache write failed", map[string]any{"key": key, "error": err.Error()})
	}
}
