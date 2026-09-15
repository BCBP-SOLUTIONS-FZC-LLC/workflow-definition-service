package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func TestModuleRepo_CreateAndGet_TenantScope(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleRepo(pool)
	ctx := context.Background()

	tenantID, userID := uuid.New(), uuid.New()
	m := &domain.Module{
		ID: uuid.New(), TenantID: &tenantID, Scope: domain.ScopeTenant,
		Name: "Approval Chain", Description: "desc", CreatedByUserID: &userID,
	}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByID(ctx, tenantID, m.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != m.Name || got.Scope != domain.ScopeTenant || got.TenantID == nil || *got.TenantID != tenantID {
		t.Errorf("unexpected module: %+v", got)
	}
}

func TestModuleRepo_CreateAndGet_GlobalScope(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleRepo(pool)
	ctx := context.Background()

	m := &domain.Module{ID: uuid.New(), Scope: domain.ScopeGlobal, Name: "Global Module"}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Any tenant can read a global-scope row.
	got, err := repo.GetByID(ctx, uuid.New(), m.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.TenantID != nil {
		t.Errorf("expected nil tenant_id for global module; got %v", got.TenantID)
	}
}

func TestModuleRepo_GetByID_NotFound(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleRepo(pool)

	_, err := repo.GetByID(context.Background(), uuid.New(), uuid.New())
	if err != domain.ErrNotFound {
		t.Errorf("GetByID = %v, want ErrNotFound", err)
	}
}

func TestModuleRepo_GetByID_CrossTenantNotFound(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleRepo(pool)
	ctx := context.Background()

	ownerTenantID := uuid.New()
	m := &domain.Module{ID: uuid.New(), TenantID: &ownerTenantID, Scope: domain.ScopeTenant, Name: "Private"}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err := repo.GetByID(ctx, uuid.New(), m.ID)
	if err != domain.ErrNotFound {
		t.Errorf("cross-tenant GetByID = %v, want ErrNotFound", err)
	}
}

func TestModuleRepo_UpdateActiveVersion(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleRepo(pool)
	versionRepo := postgres.NewModuleVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	m := &domain.Module{ID: uuid.New(), TenantID: &tenantID, Scope: domain.ScopeTenant, Name: "M"}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}
	v := newDraftModuleVersion(m.ID, &tenantID, domain.ScopeTenant)
	if err := versionRepo.Create(ctx, v); err != nil {
		t.Fatalf("create version: %v", err)
	}

	if err := repo.UpdateActiveVersion(ctx, m.ID, &v.ID); err != nil {
		t.Fatalf("UpdateActiveVersion: %v", err)
	}
	got, err := repo.GetByID(ctx, tenantID, m.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ActiveVersionID == nil || *got.ActiveVersionID != v.ID {
		t.Errorf("ActiveVersionID = %v, want %v", got.ActiveVersionID, v.ID)
	}
}

func TestModuleRepo_UpdateActiveVersion_NotFound(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleRepo(pool)

	err := repo.UpdateActiveVersion(context.Background(), uuid.New(), nil)
	if err != domain.ErrNotFound {
		t.Errorf("UpdateActiveVersion = %v, want ErrNotFound", err)
	}
}

func TestModuleRepo_List_ScopeFilter(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	tenantScope, globalScope := domain.ScopeTenant, domain.ScopeGlobal
	if err := repo.Create(ctx, &domain.Module{ID: uuid.New(), TenantID: &tenantID, Scope: tenantScope, Name: "T"}); err != nil {
		t.Fatalf("create tenant module: %v", err)
	}
	if err := repo.Create(ctx, &domain.Module{ID: uuid.New(), Scope: globalScope, Name: "G"}); err != nil {
		t.Fatalf("create global module: %v", err)
	}

	results, total, err := repo.List(ctx, tenantID, port.ModuleFilter{Scope: &globalScope})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(results) != 1 || results[0].Scope != domain.ScopeGlobal {
		t.Errorf("expected 1 global module; got total=%d results=%+v", total, results)
	}
}

func TestModuleRepo_List_SearchFilter(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	if err := repo.Create(ctx, &domain.Module{ID: uuid.New(), TenantID: &tenantID, Scope: domain.ScopeTenant, Name: "Approval Chain"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Create(ctx, &domain.Module{ID: uuid.New(), TenantID: &tenantID, Scope: domain.ScopeTenant, Name: "Unrelated"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	search := "approval"
	results, total, err := repo.List(ctx, tenantID, port.ModuleFilter{Search: &search})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(results) != 1 {
		t.Errorf("expected 1 match; got total=%d results=%+v", total, results)
	}
}

func TestModuleRepo_List_Pagination(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	for i := 0; i < 3; i++ {
		if err := repo.Create(ctx, &domain.Module{ID: uuid.New(), TenantID: &tenantID, Scope: domain.ScopeTenant, Name: "M"}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	results, total, err := repo.List(ctx, tenantID, port.ModuleFilter{Page: 1, Limit: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 3 || len(results) != 2 {
		t.Errorf("expected total=3 page-len=2; got total=%d len=%d", total, len(results))
	}
}
