package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

type CompiledPlan struct {
	WorkflowID    uuid.UUID            `json:"workflow_id"`
	VersionID     uuid.UUID            `json:"version_id"`
	VersionNumber *int32               `json:"version_number"`
	Status        domain.VersionStatus `json:"status"`
	IsValid       bool                 `json:"is_valid"`
	PlanJSON      *string              `json:"compiled_plan_json"`
}

type CompiledPlanDeps struct {
	Versions port.WorkflowVersionRepository
	Cache    port.CacheStore
	CacheTTL time.Duration
	Log      port.Logger
}

type CompiledPlanService struct {
	versions port.WorkflowVersionRepository
	cache    port.CacheStore
	cacheTTL time.Duration
	log      port.Logger
}

func NewCompiledPlanService(d CompiledPlanDeps) *CompiledPlanService {
	return &CompiledPlanService{versions: d.Versions, cache: d.Cache, cacheTTL: d.CacheTTL, log: logOrNoop(d.Log)}
}

func (s *CompiledPlanService) Get(ctx context.Context, tenantID, versionID uuid.UUID) (*CompiledPlan, error) {
	key := domain.CompiledPlanCacheKey(tenantID, versionID)
	if p, ok := s.readCache(ctx, key); ok {
		observability.IncCounter(observability.WFCacheHitsTotal)
		return p, nil
	}
	observability.IncCounter(observability.WFCacheMissesTotal)

	v, err := s.versions.GetByID(ctx, tenantID, versionID)
	if err != nil {
		return nil, fmt.Errorf("get version %s: %w", versionID, err)
	}
	p := &CompiledPlan{
		WorkflowID: v.WorkflowID, VersionID: v.ID, VersionNumber: v.VersionNumber,
		Status: v.Status, IsValid: v.IsValid, PlanJSON: v.CompiledPlanJSON,
	}
	if v.Status == domain.VersionStatusPublished && v.IsValid {
		if s.writeCache(ctx, key, p) {
			s.evictIfRevoked(ctx, tenantID, versionID, key)
		}
	}
	return p, nil
}

func (s *CompiledPlanService) evictIfRevoked(ctx context.Context, tenantID, versionID uuid.UUID, key string) {
	if s.cache == nil {
		return
	}
	current, err := s.versions.GetByID(ctx, tenantID, versionID)
	if err == nil && current.Status == domain.VersionStatusPublished && current.IsValid {
		return
	}
	if err := s.cache.Del(ctx, key); err != nil {
		s.log.Warn("compiled-plan cache eviction failed", map[string]any{"key": key, "error": err.Error()})
	}
}

func (s *CompiledPlanService) readCache(ctx context.Context, key string) (*CompiledPlan, bool) {
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
	var p CompiledPlan
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		s.log.Warn("compiled-plan cache decode failed", map[string]any{"key": key, "error": err.Error()})
		return nil, false
	}
	return &p, true
}

func (s *CompiledPlanService) writeCache(ctx context.Context, key string, p *CompiledPlan) bool {
	if s.cache == nil {
		return false
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return false
	}
	if err := s.cache.Set(ctx, key, string(raw), s.cacheTTL); err != nil {
		s.log.Warn("compiled-plan cache write failed", map[string]any{"key": key, "error": err.Error()})
		return false
	}
	return true
}
