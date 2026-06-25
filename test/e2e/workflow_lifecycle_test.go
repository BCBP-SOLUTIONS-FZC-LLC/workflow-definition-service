//go:build integration

package e2e_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

// TestE2E_WorkflowLifecycle exercises the full state machine through the real
// service layer with a real Postgres container:
//
//  1. Create workflow + initial DRAFT version
//  2. Update the draft's metadata
//  3. Publish DRAFT → PUBLISHED (version_number=1)
//  4. Promote to active
//  5. Init a second draft
//  6. Publish second draft (version_number=2)
//  7. Archive — active version PUBLISHED → ARCHIVED
func TestE2E_WorkflowLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := fixtures.NewTestPool(t)
	svcs := wireServices(t, pool, nil)

	tenantID := uuid.New()
	userID := uuid.New()

	// 1. Create workflow + initial DRAFT
	wf, _, err := svcs.workflow.Create(ctx, tenantID, userID,
		"e2e-lifecycle-test", "E2E Lifecycle Test", "", minimalBPMN, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if wf.ID == uuid.Nil {
		t.Fatal("expected non-nil workflow ID")
	}

	// Retrieve initial draft
	draft, err := svcs.draft.Get(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("Get draft: %v", err)
	}
	if draft.Status != domain.VersionStatusDraft {
		t.Fatalf("expected DRAFT status, got %s", draft.Status)
	}

	// 2. Update the draft metadata
	updatedName := "E2E Lifecycle Test (updated)"
	if _, err := svcs.draft.Update(ctx, tenantID, userID, wf.ID, service.UpdateDraftReq{
		Name: &updatedName,
	}); err != nil {
		t.Fatalf("Update draft: %v", err)
	}

	// 3. Publish DRAFT → PUBLISHED
	published, err := svcs.version.Publish(ctx, tenantID, userID, wf.ID, draft.ID, false)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if published.Status != domain.VersionStatusPublished {
		t.Fatalf("expected PUBLISHED, got %s", published.Status)
	}
	if published.VersionNumber == nil || *published.VersionNumber != 1 {
		t.Fatalf("expected version_number=1, got %v", published.VersionNumber)
	}
	if published.CompiledPlanJSON == nil {
		t.Fatal("expected non-nil compiled_plan_json after publish")
	}

	// 4. Promote to active
	if _, err := svcs.version.Promote(ctx, tenantID, userID, wf.ID, published.ID); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// Verify workflow now has an active version
	wfDetail, _, err := svcs.workflow.Get(ctx, tenantID, wf.ID, 10)
	if err != nil {
		t.Fatalf("Get workflow: %v", err)
	}
	if wfDetail.ActiveVersionID == nil {
		t.Fatal("expected active_version_id to be set after Promote")
	}
	if *wfDetail.ActiveVersionID != published.ID {
		t.Errorf("active_version_id = %v, want %v", *wfDetail.ActiveVersionID, published.ID)
	}

	// 5. Init a second draft
	if _, err := svcs.draft.Init(ctx, tenantID, userID, wf.ID); err != nil {
		t.Fatalf("Init draft: %v", err)
	}

	draft2, err := svcs.draft.Get(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("Get second draft: %v", err)
	}
	if draft2.Status != domain.VersionStatusDraft {
		t.Fatalf("expected DRAFT status for second draft, got %s", draft2.Status)
	}

	// 6. Publish second draft (version_number=2)
	published2, err := svcs.version.Publish(ctx, tenantID, userID, wf.ID, draft2.ID, false)
	if err != nil {
		t.Fatalf("Publish second draft: %v", err)
	}
	if published2.VersionNumber == nil || *published2.VersionNumber != 2 {
		t.Fatalf("expected version_number=2, got %v", published2.VersionNumber)
	}

	// 7. Archive — active version PUBLISHED → ARCHIVED
	if err := svcs.workflow.Archive(ctx, tenantID, userID, wf.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	// V2 (published2) was the active version at archive time — it should be ARCHIVED.
	// V1 (published) was superseded when Publish V2 set active_version_id to V2.
	archived, err := svcs.version.Get(ctx, tenantID, wf.ID, published2.ID)
	if err != nil {
		t.Fatalf("Get archived version: %v", err)
	}
	if archived.Status != domain.VersionStatusArchived {
		t.Fatalf("expected ARCHIVED, got %s", archived.Status)
	}

	wfAfter, _, err := svcs.workflow.Get(ctx, tenantID, wf.ID, 1)
	if err != nil {
		t.Fatalf("Get workflow after archive: %v", err)
	}
	if wfAfter.ActiveVersionID != nil {
		t.Errorf("expected active_version_id=nil after archive, got %v", *wfAfter.ActiveVersionID)
	}
}

// TestE2E_RLSCrossTenantIsolation seeds workflows for two tenants via a
// superuser connection (no GUC → RLS bypassed) and then queries each using a
// tenant-scoped pool. Verifies that tenant A cannot read tenant B's rows.
func TestE2E_RLSCrossTenantIsolation(t *testing.T) {
	ctx := context.Background()

	// NewTestPoolAndDSN returns a superuser pool + DSN so we can create
	// additional tenant-scoped pools against the same container.
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)

	tenantA := uuid.New()
	tenantB := uuid.New()
	userA := uuid.New()
	userB := uuid.New()

	// Seed data for both tenants using the superuser pool (bypasses RLS).
	superRepo := pgadapter.NewWorkflowRepo(superPool)

	wfA := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantA, CreatedByUserID: userA,
		BusinessKey: "rls-test-tenant-a",
		Name:        "RLS Test Tenant A",
	}
	if err := superRepo.Create(ctx, wfA); err != nil {
		t.Fatalf("seed wfA: %v", err)
	}

	wfB := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantB, CreatedByUserID: userB,
		BusinessKey: "rls-test-tenant-b",
		Name:        "RLS Test Tenant B",
	}
	if err := superRepo.Create(ctx, wfB); err != nil {
		t.Fatalf("seed wfB: %v", err)
	}

	// Build tenant-scoped pools by providing a GUCProvider to pgcommon.
	poolA := tenantScopedPool(t, ctx, dsn, tenantA, userA)
	poolB := tenantScopedPool(t, ctx, dsn, tenantB, userB)

	repoA := pgadapter.NewWorkflowRepo(poolA)
	repoB := pgadapter.NewWorkflowRepo(poolB)

	// Tenant A can read its own workflow.
	if _, err := repoA.GetByID(ctx, tenantA, wfA.ID); err != nil {
		t.Errorf("tenant A should see its own workflow: %v", err)
	}

	// Tenant A cannot read tenant B's workflow (both WHERE and RLS block it).
	if _, err := repoA.GetByID(ctx, tenantA, wfB.ID); err == nil {
		t.Error("tenant A must NOT see tenant B's workflow, but got no error")
	}

	// Tenant B can read its own workflow.
	if _, err := repoB.GetByID(ctx, tenantB, wfB.ID); err != nil {
		t.Errorf("tenant B should see its own workflow: %v", err)
	}

	// Tenant B cannot read tenant A's workflow.
	if _, err := repoB.GetByID(ctx, tenantB, wfA.ID); err == nil {
		t.Error("tenant B must NOT see tenant A's workflow, but got no error")
	}

	// List is also scoped: tenant A sees only its own workflows.
	svcsA := wireServices(t, poolA, nil)
	list, _, err := svcsA.workflow.List(ctx, tenantA, port.WorkflowFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListWorkflows tenantA: %v", err)
	}
	for _, w := range list {
		if w.TenantID != tenantA {
			t.Errorf("tenant A list contains workflow owned by tenant %v", w.TenantID)
		}
	}
}

// tenantScopedPool creates a pool against the given DSN with a GUCProvider
// that injects tenantID and userID on every acquired connection, which causes
// the database's RLS policies to be enforced for that tenant.
func tenantScopedPool(t *testing.T, ctx context.Context, dsn string, tenantID, userID uuid.UUID) *pgcommon.Pool {
	t.Helper()

	pool, err := pgcommon.NewPool(ctx, pgcommon.Config{
		DSN: dsn,
		GUCProvider: func(context.Context) (pgdomain.GUCSet, bool) {
			return pgdomain.GUCSet{
				TenantID: tenantID.String(),
				UserID:   userID.String(),
			}, true
		},
	})
	if err != nil {
		t.Fatalf("tenantScopedPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
