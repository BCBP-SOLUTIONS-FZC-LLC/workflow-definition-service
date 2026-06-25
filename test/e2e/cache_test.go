//go:build integration

package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

// TestE2E_Valkey_CacheIntegration verifies that WorkflowService.Archive()
// invalidates the compiled-plan cache entry for the active version.
//
// The cache is written lazily (on first GetCompiledWorkflow gRPC call), so
// this test seeds the cache manually before archiving and asserts it is gone.
//
//  1. Publish + promote a version with a real Valkey-backed CacheStore
//  2. Manually seed the cache: wf:plan:<tenantID>:<versionID>
//  3. Assert cache hit
//  4. Archive the workflow
//  5. Assert cache miss (Archive calls cache.Del)
func TestE2E_Valkey_CacheIntegration(t *testing.T) {
	ctx := context.Background()

	pool := fixtures.NewTestPool(t)
	cache := fixtures.NewTestValkey(t)
	svcs := wireServices(t, pool, cache)

	tenantID := uuid.New()
	userID := uuid.New()

	// 1. Create workflow + publish + promote
	wf, _, err := svcs.workflow.Create(ctx, tenantID, userID,
		"e2e-cache-test", "E2E Cache Test", "", minimalBPMN, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	draft, err := svcs.draft.Get(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("Get draft: %v", err)
	}
	published, err := svcs.version.Publish(ctx, tenantID, userID, wf.ID, draft.ID, false)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, err := svcs.version.Promote(ctx, tenantID, userID, wf.ID, published.ID); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// 2. Seed the cache manually (simulates the gRPC GetCompiledWorkflow lazy-load path)
	cacheKey := domain.CompiledPlanCacheKey(tenantID, published.ID)
	if err := cache.Set(ctx, cacheKey, `{"steps":[]}`, time.Minute); err != nil {
		t.Fatalf("cache.Set: %v", err)
	}

	// 3. Assert cache hit
	val, err := cache.Get(ctx, cacheKey)
	if err != nil {
		t.Fatalf("cache.Get: %v", err)
	}
	if val == "" {
		t.Fatal("expected compiled plan in cache after Set, got empty")
	}

	// 4. Archive the workflow — WorkflowService.Archive calls cache.Del
	if err := svcs.workflow.Archive(ctx, tenantID, userID, wf.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	// 5. Assert cache miss
	after, err := cache.Get(ctx, cacheKey)
	if err != nil {
		t.Fatalf("cache.Get after archive: %v", err)
	}
	if after != "" {
		t.Errorf("expected cache miss after Archive, got %q", after)
	}
}
