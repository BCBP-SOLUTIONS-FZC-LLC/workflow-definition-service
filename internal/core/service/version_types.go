package service

import (
	"context"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func resolveTemplateLimit(ctx context.Context, limits port.PlanLimits, planCode string) (*int64, error) {
	if limits == nil {
		return nil, fmt.Errorf("%w: no plan limit source is configured", domain.ErrUpstreamUnavailable)
	}
	limit, err := limits.WorkflowTemplateLimit(ctx, planCode)
	if err != nil {
		return nil, fmt.Errorf("resolve workflow template limit: %w", err)
	}
	return limit, nil
}

func enforceWorkflowQuota(ctx context.Context, workflows port.WorkflowRepository, tenantID uuid.UUID, limit *int64) error {
	if limit == nil {
		return nil
	}
	count, err := workflows.CountByTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("count workflows: %w", err)
	}
	if count >= *limit {
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
