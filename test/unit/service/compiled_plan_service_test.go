package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type planVersions struct {
	port.WorkflowVersionRepository
	get   func(tenantID, versionID uuid.UUID) (*domain.WorkflowVersion, error)
	reads int
}

func (p *planVersions) GetByID(_ context.Context, tenantID, versionID uuid.UUID) (*domain.WorkflowVersion, error) {
	p.reads++
	return p.get(tenantID, versionID)
}

type planCache struct {
	port.CacheStore
	store   map[string]string
	sets    int
	getErr  error
	setErr  error
	lastTTL time.Duration
}

func newPlanCache() *planCache { return &planCache{store: map[string]string{}} }

func (c *planCache) Get(_ context.Context, key string) (string, error) {
	return c.store[key], c.getErr
}

func (c *planCache) Set(_ context.Context, key, value string, ttl time.Duration) error {
	c.sets++
	c.lastTTL = ttl
	if c.setErr != nil {
		return c.setErr
	}
	c.store[key] = value
	return nil
}

func publishedPlanVersion(versionID, workflowID uuid.UUID, planJSON string) *domain.WorkflowVersion {
	n := int32(3)
	return &domain.WorkflowVersion{
		ID: versionID, WorkflowID: workflowID, Status: domain.VersionStatusPublished, IsValid: true,
		VersionNumber: &n, CompiledPlanJSON: &planJSON,
	}
}

func TestCompiledPlan_PublishedVersion(t *testing.T) {
	tenantID, versionID, workflowID := uuid.New(), uuid.New(), uuid.New()
	plan := `{"main_plan":"m","schema_version":1}`
	versions := &planVersions{get: func(gotTenant, gotVersion uuid.UUID) (*domain.WorkflowVersion, error) {
		if gotTenant != tenantID || gotVersion != versionID {
			t.Errorf("read tenant %s version %s", gotTenant, gotVersion)
		}
		return publishedPlanVersion(versionID, workflowID, plan), nil
	}}
	svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions})

	got, err := svc.Get(context.Background(), tenantID, versionID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.WorkflowID != workflowID || got.VersionID != versionID || got.VersionNumber == nil || *got.VersionNumber != 3 ||
		got.Status != domain.VersionStatusPublished || !got.IsValid || got.PlanJSON == nil || *got.PlanJSON != plan {
		t.Errorf("plan = %+v", got)
	}
}

// A draft has no stored plan and no number; the read returns it as it is and
// the Execution Service refuses it for not being published.
func TestCompiledPlan_DraftHasNoPlan(t *testing.T) {
	versionID := uuid.New()
	versions := &planVersions{get: func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
		return &domain.WorkflowVersion{ID: versionID, WorkflowID: uuid.New(), Status: domain.VersionStatusDraft}, nil
	}}
	got, err := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions}).Get(context.Background(), uuid.New(), versionID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.PlanJSON != nil || got.VersionNumber != nil || got.Status != domain.VersionStatusDraft {
		t.Errorf("draft = %+v", got)
	}
}

func TestCompiledPlan_NotFound(t *testing.T) {
	versions := &planVersions{get: func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) { return nil, domain.ErrNotFound }}
	_, err := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions}).Get(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCompiledPlan_CachePopulateThenHit(t *testing.T) {
	tenantID, versionID, workflowID := uuid.New(), uuid.New(), uuid.New()
	versions := &planVersions{get: func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
		return publishedPlanVersion(versionID, workflowID, `{"schema_version":1}`), nil
	}}
	cache := newPlanCache()
	svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: cache, CacheTTL: time.Hour})

	first, err := svc.Get(context.Background(), tenantID, versionID)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	second, err := svc.Get(context.Background(), tenantID, versionID)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if versions.reads != 1 || cache.sets != 1 || cache.lastTTL != time.Hour {
		t.Errorf("reads %d, sets %d, ttl %s; want one read, one populate at 1h", versions.reads, cache.sets, cache.lastTTL)
	}
	if _, ok := cache.store[domain.CompiledPlanCacheKey(tenantID, versionID)]; !ok {
		t.Error("the plan is not cached under its tenant-scoped key")
	}
	if *second.PlanJSON != *first.PlanJSON || *second.VersionNumber != 3 || second.WorkflowID != workflowID {
		t.Errorf("cached plan = %+v, want the one read", second)
	}
}

// The cache fails open: a read error, an undecodable entry and a write error
// each fall back to, or keep, the database read.
func TestCompiledPlan_CacheFailsOpen(t *testing.T) {
	tenantID, versionID := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name  string
		cache *planCache
	}{
		{"read error", &planCache{store: map[string]string{}, getErr: errors.New("valkey down")}},
		{"undecodable entry", &planCache{store: map[string]string{domain.CompiledPlanCacheKey(tenantID, versionID): "{not json"}}},
		{"write error", &planCache{store: map[string]string{}, setErr: errors.New("valkey down")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			versions := &planVersions{get: func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
				return publishedPlanVersion(versionID, uuid.New(), `{"schema_version":1}`), nil
			}}
			svc := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions, Cache: tc.cache, CacheTTL: time.Hour})
			got, err := svc.Get(context.Background(), tenantID, versionID)
			if err != nil || got.PlanJSON == nil || versions.reads != 1 {
				t.Errorf("Get = %+v, %v after %d reads; want the database's plan", got, err, versions.reads)
			}
		})
	}
}

func TestCompiledPlan_RepositoryErrorIsReturned(t *testing.T) {
	boom := errors.New("connection reset")
	versions := &planVersions{get: func(uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) { return nil, boom }}
	if _, err := service.NewCompiledPlanService(service.CompiledPlanDeps{Versions: versions}).Get(context.Background(), uuid.New(), uuid.New()); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the repository's", err)
	}
}
