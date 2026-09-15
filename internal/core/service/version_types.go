package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

// workflowQuotaLimit hardcodes plan-tier quotas pending a billing-service
// integration; replace this switch with a gRPC/HTTP call once that lands.
func workflowQuotaLimit(planTier string) int64 {
	switch strings.ToLower(planTier) {
	case "starter":
		return 5
	case "pro":
		return 50
	case "enterprise":
		return -1
	default:
		return 5
	}
}

// enforceWorkflowQuota is shared by WorkflowService.Create and
// VersionService.Clone, both of which run it inside the same
// SERIALIZABLE-retry transaction as their insert (LLD §5.6.2).
func enforceWorkflowQuota(ctx context.Context, workflows port.WorkflowRepository, tenantID uuid.UUID, planTier string) error {
	limit := workflowQuotaLimit(planTier)
	if limit <= 0 {
		return nil
	}
	count, err := workflows.CountByTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("count workflows: %w", err)
	}
	if count >= limit {
		return domain.ErrPlanQuotaExceeded
	}
	return nil
}

// invalidateCompiledPlanCache deletes domain.CompiledPlanCacheKey's entry,
// logging (not returning) a failure since the cache is best-effort.
func invalidateCompiledPlanCache(ctx context.Context, cache port.CacheStore, log port.Logger, tenantID, versionID uuid.UUID) {
	if cache == nil {
		return
	}
	if err := cache.Del(ctx, domain.CompiledPlanCacheKey(tenantID, versionID)); err != nil {
		log.Warn("compiled-plan cache invalidation failed", map[string]any{
			"version_id": versionID.String(), "error": err.Error(),
		})
	}
}

type ChangeType string

const (
	ChangeTypeStructural   ChangeType = "STRUCTURAL"
	ChangeTypeMetadataOnly ChangeType = "METADATA_ONLY"
)

type DiffResult struct {
	WorkflowID      uuid.UUID
	BaseVersionID   uuid.UUID
	TargetVersionID uuid.UUID
	ChangeType      ChangeType
	Changes         DiffChanges
}

type DiffChanges struct {
	AddedDepartments   []string
	RemovedDepartments []string
	StepChanges        []StepChange
	MetadataOnly       bool
}

type StepChange struct {
	Description string
	Before      *dsl.ExecutionStep
	After       *dsl.ExecutionStep
}

type CloneReq struct {
	NewKey         string
	NewName        string
	NewDescription string
}
