package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func TestStarterRepo_CreateAndGet_TenantScope(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewStarterRepo(pool)
	ctx := context.Background()

	tenantID, userID := uuid.New(), uuid.New()
	st := &domain.StarterTemplate{
		ID: uuid.New(), TenantID: &tenantID, Scope: domain.ScopeTenant,
		Name: "Onboarding", Category: "hr", BPMNXML: "<bpmn/>", CreatedByUserID: &userID,
	}
	if err := repo.Create(ctx, st); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByID(ctx, tenantID, st.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != st.Name || got.Category != "hr" || got.BPMNXML != "<bpmn/>" {
		t.Errorf("unexpected starter: %+v", got)
	}
}

func TestStarterRepo_CreateAndGet_GlobalScope(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewStarterRepo(pool)
	ctx := context.Background()

	st := &domain.StarterTemplate{ID: uuid.New(), Scope: domain.ScopeGlobal, Name: "Global Starter", BPMNXML: "<bpmn/>"}
	if err := repo.Create(ctx, st); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByID(ctx, uuid.New(), st.ID)
	if err != nil {
		t.Fatalf("GetByID from unrelated tenant: %v", err)
	}
	if got.TenantID != nil {
		t.Errorf("expected nil tenant_id; got %v", got.TenantID)
	}
}

func TestStarterRepo_GetByID_NotFound(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewStarterRepo(pool)

	_, err := repo.GetByID(context.Background(), uuid.New(), uuid.New())
	if err != domain.ErrNotFound {
		t.Errorf("GetByID = %v, want ErrNotFound", err)
	}
}

func TestStarterRepo_GetByID_CrossTenantNotFound(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewStarterRepo(pool)
	ctx := context.Background()

	ownerTenantID := uuid.New()
	st := &domain.StarterTemplate{ID: uuid.New(), TenantID: &ownerTenantID, Scope: domain.ScopeTenant, Name: "Private", BPMNXML: "<bpmn/>"}
	if err := repo.Create(ctx, st); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err := repo.GetByID(ctx, uuid.New(), st.ID)
	if err != domain.ErrNotFound {
		t.Errorf("cross-tenant GetByID = %v, want ErrNotFound", err)
	}
}

func TestStarterRepo_Delete(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewStarterRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	st := &domain.StarterTemplate{ID: uuid.New(), TenantID: &tenantID, Scope: domain.ScopeTenant, Name: "S", BPMNXML: "<bpmn/>"}
	if err := repo.Create(ctx, st); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, st.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, tenantID, st.ID); err != domain.ErrNotFound {
		t.Errorf("GetByID after delete = %v, want ErrNotFound", err)
	}
}

func TestStarterRepo_Delete_NotFound(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewStarterRepo(pool)

	err := repo.Delete(context.Background(), uuid.New())
	if err != domain.ErrNotFound {
		t.Errorf("Delete = %v, want ErrNotFound", err)
	}
}

func TestStarterRepo_List_CategoryFilter(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewStarterRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	if err := repo.Create(ctx, &domain.StarterTemplate{ID: uuid.New(), TenantID: &tenantID, Scope: domain.ScopeTenant, Name: "A", Category: "hr", BPMNXML: "<bpmn/>"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Create(ctx, &domain.StarterTemplate{ID: uuid.New(), TenantID: &tenantID, Scope: domain.ScopeTenant, Name: "B", Category: "finance", BPMNXML: "<bpmn/>"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	cat := "hr"
	results, total, err := repo.List(ctx, tenantID, port.StarterFilter{Category: &cat})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(results) != 1 || results[0].Category != "hr" {
		t.Errorf("expected 1 hr starter; got total=%d results=%+v", total, results)
	}
}

func TestStarterRepo_List_SearchFilter(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewStarterRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	if err := repo.Create(ctx, &domain.StarterTemplate{ID: uuid.New(), TenantID: &tenantID, Scope: domain.ScopeTenant, Name: "Onboarding Flow", BPMNXML: "<bpmn/>"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Create(ctx, &domain.StarterTemplate{ID: uuid.New(), TenantID: &tenantID, Scope: domain.ScopeTenant, Name: "Unrelated", BPMNXML: "<bpmn/>"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	search := "onboarding"
	results, total, err := repo.List(ctx, tenantID, port.StarterFilter{Search: &search})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(results) != 1 {
		t.Errorf("expected 1 match; got total=%d results=%+v", total, results)
	}
}
