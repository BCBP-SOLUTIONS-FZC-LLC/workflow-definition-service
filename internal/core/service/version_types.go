package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

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

type evictionError struct{ err error }

func (e *evictionError) Error() string { return e.err.Error() }

func (e *evictionError) Unwrap() error { return e.err }

func evictCompiledPlan(ctx context.Context, cache port.CacheStore, tenantID, versionID uuid.UUID) error {
	if cache == nil {
		return nil
	}
	if err := cache.Del(ctx, domain.CompiledPlanCacheKey(tenantID, versionID)); err != nil {
		return &evictionError{err: fmt.Errorf("evict compiled plan %s: %w", versionID, err)}
	}
	return nil
}

func invalidateCompiledPlanCache(ctx context.Context, cache port.CacheStore, log port.Logger, tenantID, versionID uuid.UUID) {
	if err := evictCompiledPlan(ctx, cache, tenantID, versionID); err != nil {
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
	StageChanges       []StageChange
	MetadataOnly       bool
}

type StageChange struct {
	NodeKey     string
	Description string
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
