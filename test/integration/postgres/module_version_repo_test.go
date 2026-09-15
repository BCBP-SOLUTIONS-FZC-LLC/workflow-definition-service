package postgres_test

import (
	"context"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func newTestModule(t *testing.T, ctx context.Context, pool *pgcommon.Pool, tenantID uuid.UUID) *domain.Module {
	t.Helper()
	repo := postgres.NewModuleRepo(pool)
	m := &domain.Module{ID: uuid.New(), TenantID: &tenantID, Scope: domain.ScopeTenant, Name: "M"}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatalf("create module: %v", err)
	}
	return m
}

func newDraftModuleVersion(moduleID uuid.UUID, tenantID *uuid.UUID, scope domain.CatalogScope) *domain.ModuleVersion {
	return &domain.ModuleVersion{
		ID: uuid.New(), ModuleID: moduleID, TenantID: tenantID, Scope: scope,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>", ProcessID: "P1", IsValid: true,
	}
}

func TestModuleVersionRepo_CreateAndGet(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	mod := newTestModule(t, ctx, pool, tenantID)
	v := newDraftModuleVersion(mod.ID, &tenantID, domain.ScopeTenant)
	if err := repo.Create(ctx, v); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByID(ctx, tenantID, v.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ModuleID != mod.ID || got.ProcessID != "P1" || got.Status != domain.VersionStatusDraft {
		t.Errorf("unexpected version: %+v", got)
	}
}

func TestModuleVersionRepo_GetByID_NotFound(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleVersionRepo(pool)

	_, err := repo.GetByID(context.Background(), uuid.New(), uuid.New())
	if err != domain.ErrNotFound {
		t.Errorf("GetByID = %v, want ErrNotFound", err)
	}
}

func TestModuleVersionRepo_ListByModule(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	mod := newTestModule(t, ctx, pool, tenantID)
	draft := newDraftModuleVersion(mod.ID, &tenantID, domain.ScopeTenant)
	if err := repo.Create(ctx, draft); err != nil {
		t.Fatalf("create draft: %v", err)
	}

	results, total, err := repo.ListByModule(ctx, tenantID, mod.ID, 1, 20)
	if err != nil {
		t.Fatalf("ListByModule: %v", err)
	}
	if total != 1 || len(results) != 1 {
		t.Errorf("expected 1 version; got total=%d len=%d", total, len(results))
	}
}

func TestModuleVersionRepo_Publish(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	mod := newTestModule(t, ctx, pool, tenantID)
	draft := newDraftModuleVersion(mod.ID, &tenantID, domain.ScopeTenant)
	if err := repo.Create(ctx, draft); err != nil {
		t.Fatalf("create draft: %v", err)
	}

	if err := repo.Publish(ctx, draft.ID, 1); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got, err := repo.GetByID(ctx, tenantID, draft.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != domain.VersionStatusPublished || got.VersionNumber == nil || *got.VersionNumber != 1 {
		t.Errorf("unexpected published version: %+v", got)
	}
}

func TestModuleVersionRepo_Publish_NotDraft(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	mod := newTestModule(t, ctx, pool, tenantID)
	draft := newDraftModuleVersion(mod.ID, &tenantID, domain.ScopeTenant)
	if err := repo.Create(ctx, draft); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if err := repo.Publish(ctx, draft.ID, 1); err != nil {
		t.Fatalf("first publish: %v", err)
	}

	if err := repo.Publish(ctx, draft.ID, 2); err != domain.ErrVersionNotDraft {
		t.Errorf("re-publish = %v, want ErrVersionNotDraft", err)
	}
}

func TestModuleVersionRepo_Publish_NotFound(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleVersionRepo(pool)

	err := repo.Publish(context.Background(), uuid.New(), 1)
	if err != domain.ErrNotFound {
		t.Errorf("Publish = %v, want ErrNotFound", err)
	}
}

func TestModuleVersionRepo_Archive(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	mod := newTestModule(t, ctx, pool, tenantID)
	draft := newDraftModuleVersion(mod.ID, &tenantID, domain.ScopeTenant)
	if err := repo.Create(ctx, draft); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if err := repo.Publish(ctx, draft.ID, 1); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if err := repo.Archive(ctx, draft.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	got, err := repo.GetByID(ctx, tenantID, draft.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != domain.VersionStatusArchived {
		t.Errorf("Status = %v, want ARCHIVED", got.Status)
	}
}

func TestModuleVersionRepo_Archive_NotPublished(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	mod := newTestModule(t, ctx, pool, tenantID)
	draft := newDraftModuleVersion(mod.ID, &tenantID, domain.ScopeTenant)
	if err := repo.Create(ctx, draft); err != nil {
		t.Fatalf("create draft: %v", err)
	}

	if err := repo.Archive(ctx, draft.ID); err != domain.ErrVersionNotPublished {
		t.Errorf("Archive DRAFT = %v, want ErrVersionNotPublished", err)
	}
}

func TestModuleVersionRepo_NextVersionNumber(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	mod := newTestModule(t, ctx, pool, tenantID)
	draft := newDraftModuleVersion(mod.ID, &tenantID, domain.ScopeTenant)
	if err := repo.Create(ctx, draft); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if err := repo.Publish(ctx, draft.ID, 1); err != nil {
		t.Fatalf("publish: %v", err)
	}

	n, err := repo.NextVersionNumber(ctx, mod.ID)
	if err != nil {
		t.Fatalf("NextVersionNumber: %v", err)
	}
	if n != 2 {
		t.Errorf("NextVersionNumber = %d, want 2", n)
	}
}

func TestModuleVersionRepo_DraftAlreadyExists(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleVersionRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	mod := newTestModule(t, ctx, pool, tenantID)
	v1 := newDraftModuleVersion(mod.ID, &tenantID, domain.ScopeTenant)
	if err := repo.Create(ctx, v1); err != nil {
		t.Fatalf("create first draft: %v", err)
	}

	v2 := newDraftModuleVersion(mod.ID, &tenantID, domain.ScopeTenant)
	if err := repo.Create(ctx, v2); err != domain.ErrDraftAlreadyExists {
		t.Errorf("create second draft = %v, want ErrDraftAlreadyExists", err)
	}
}
