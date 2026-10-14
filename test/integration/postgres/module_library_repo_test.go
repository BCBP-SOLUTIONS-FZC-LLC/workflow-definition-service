package postgres_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

// libraryModule creates a module with processID, owned by tenantID or global
// when tenantID is nil, and publishes one version per entry in published,
// the last becoming the active version.
func libraryModule(t *testing.T, pool *pgcommon.Pool, tenantID *uuid.UUID, processID string, published int) (*domain.Module, []*domain.ModuleVersion) {
	t.Helper()
	ctx := context.Background()
	modules, versions := postgres.NewModuleRepo(pool), postgres.NewModuleVersionRepo(pool)
	scope := domain.ScopeTenant
	if tenantID == nil {
		scope = domain.ScopeGlobal
	}
	m := &domain.Module{ID: uuid.New(), TenantID: tenantID, Scope: scope, Name: processID, ProcessID: processID}
	if err := modules.Create(ctx, m); err != nil {
		t.Fatalf("create module %s: %v", processID, err)
	}
	var out []*domain.ModuleVersion
	for n := int32(1); n <= int32(published); n++ {
		v := &domain.ModuleVersion{
			ID: uuid.New(), ModuleID: m.ID, TenantID: tenantID, Scope: scope,
			Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>", ProcessID: processID, IsValid: true,
		}
		if err := versions.Create(ctx, v); err != nil {
			t.Fatalf("create module version: %v", err)
		}
		if err := versions.Publish(ctx, callerTenant(tenantID), v.ID, n); err != nil {
			t.Fatalf("publish module version: %v", err)
		}
		v.VersionNumber = &n
		if err := modules.UpdateActiveVersion(ctx, m.ID, &v.ID); err != nil {
			t.Fatalf("set active module version: %v", err)
		}
		out = append(out, v)
	}
	return m, out
}

func TestModuleRepo_ProcessIDUniqueWithinEachScope(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleRepo(pool)
	ctx := context.Background()
	tenantA, tenantB := uuid.New(), uuid.New()
	libraryModule(t, pool, &tenantA, "Process_Review", 0)
	libraryModule(t, pool, nil, "Process_Global", 0)

	for name, m := range map[string]*domain.Module{
		"same tenant": {ID: uuid.New(), TenantID: &tenantA, Scope: domain.ScopeTenant, Name: "x", ProcessID: "Process_Review"},
		"global":      {ID: uuid.New(), Scope: domain.ScopeGlobal, Name: "x", ProcessID: "Process_Global"},
	} {
		if err := repo.Create(ctx, m); !errors.Is(err, domain.ErrModuleProcessIDTaken) {
			t.Errorf("%s: Create = %v, want ErrModuleProcessIDTaken", name, err)
		}
	}
	other := &domain.Module{ID: uuid.New(), TenantID: &tenantB, Scope: domain.ScopeTenant, Name: "x", ProcessID: "Process_Review"}
	if err := repo.Create(ctx, other); err != nil {
		t.Errorf("another tenant may reuse a tenant module's process id: %v", err)
	}
}

func TestModuleRepo_ProcessIDInUse(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleRepo(pool)
	ctx := context.Background()
	tenantA, tenantB := uuid.New(), uuid.New()
	libraryModule(t, pool, &tenantA, "Process_A", 0)
	libraryModule(t, pool, nil, "Process_Global", 0)

	cases := []struct {
		name      string
		tenantID  *uuid.UUID
		processID string
		want      bool
	}{
		{"own module", &tenantA, "Process_A", true},
		{"global module", &tenantB, "Process_Global", true},
		{"another tenant's module", &tenantB, "Process_A", false},
		{"unused", &tenantA, "Process_New", false},
		{"system pool sees tenant modules", nil, "Process_A", true},
		{"system pool, unused", nil, "Process_New", false},
	}
	for _, tc := range cases {
		got, err := repo.ProcessIDInUse(ctx, tc.tenantID, tc.processID)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: ProcessIDInUse = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestModuleVersionRepo_Resolve(t *testing.T) {
	pool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	tenantA, tenantB, userA := uuid.New(), uuid.New(), uuid.New()
	_, own := libraryModule(t, pool, &tenantA, "Process_Own", 3)
	_, global := libraryModule(t, pool, nil, "Process_Global", 2)
	libraryModule(t, pool, &tenantB, "Process_Other", 1)

	draftModule, _ := libraryModule(t, pool, &tenantA, "Process_Draft", 1)
	draft := &domain.ModuleVersion{
		ID: uuid.New(), ModuleID: draftModule.ID, TenantID: &tenantA, Scope: domain.ScopeTenant,
		Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>", ProcessID: "Process_Draft", IsValid: true,
	}
	if err := postgres.NewModuleVersionRepo(pool).Create(ctx, draft); err != nil {
		t.Fatalf("create draft: %v", err)
	}

	archived, archivedVersions := libraryModule(t, pool, &tenantA, "Process_Archived", 1)
	if err := postgres.NewModuleVersionRepo(pool).Archive(ctx, tenantA, archivedVersions[0].ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if err := postgres.NewModuleRepo(pool).UpdateActiveVersion(ctx, archived.ID, nil); err != nil {
		t.Fatalf("clear active version: %v", err)
	}

	// Tenant reads go through the RLS pool, as in production.
	tenantRepo := postgres.NewModuleVersionRepo(fixtures.NewRestrictedRolePool(t, dsn, false, &tenantA, &userA))
	found := []struct {
		name      string
		processID string
		version   int32
		want      uuid.UUID
	}{
		{"pinned", "Process_Own", 2, own[1].ID},
		{"latest is the active version", "Process_Own", 0, own[2].ID},
		{"a global module, latest", "Process_Global", 0, global[1].ID},
		{"a global module, pinned", "Process_Global", 1, global[0].ID},
	}
	for _, tc := range found {
		v, err := tenantRepo.Resolve(ctx, tenantA, tc.processID, tc.version)
		if err != nil {
			t.Errorf("%s: Resolve = %v", tc.name, err)
			continue
		}
		if v.ID != tc.want || v.VersionNumber == nil || v.ProcessID != tc.processID {
			t.Errorf("%s: resolved %+v, want version %s", tc.name, v, tc.want)
		}
	}

	notFound := []struct {
		name      string
		processID string
		version   int32
	}{
		{"a version never published", "Process_Own", 4},
		{"another tenant's module", "Process_Other", 0},
		{"an archived module, latest", "Process_Archived", 0},
		{"an archived module, pinned", "Process_Archived", 1},
		{"no such module", "Process_None", 0},
	}
	for _, tc := range notFound {
		if _, err := tenantRepo.Resolve(ctx, tenantA, tc.processID, tc.version); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: Resolve = %v, want ErrNotFound", tc.name, err)
		}
	}

	// A global module being authored resolves on the system pool as uuid.Nil,
	// which sees global modules only.
	systemRepo := postgres.NewModuleVersionRepo(pool)
	if _, err := systemRepo.Resolve(ctx, uuid.Nil, "Process_Global", 0); err != nil {
		t.Errorf("global scope: Resolve(Process_Global) = %v", err)
	}
	if _, err := systemRepo.Resolve(ctx, uuid.Nil, "Process_Own", 0); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("global scope: Resolve(Process_Own) = %v, want ErrNotFound", err)
	}
}

func TestWorkflowVersionRepo_ModuleRefsAndUsages(t *testing.T) {
	pool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	tenantA, tenantB, userA := uuid.New(), uuid.New(), uuid.New()
	review, reviewVersions := libraryModule(t, pool, nil, "Process_Review", 2)
	check, checkVersions := libraryModule(t, pool, nil, "Process_Check", 1)
	ref := func(m *domain.Module, v *domain.ModuleVersion) domain.ModuleRef {
		return domain.ModuleRef{ModuleID: m.ID, VersionID: v.ID, ProcessID: m.ProcessID, VersionNumber: *v.VersionNumber}
	}

	publish := func(tenantID uuid.UUID, key string, refs []domain.ModuleRef) *domain.WorkflowVersion {
		t.Helper()
		wf := &domain.Workflow{ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(), BusinessKey: key, Name: key}
		if err := postgres.NewWorkflowRepo(pool).Create(ctx, wf); err != nil {
			t.Fatalf("create workflow: %v", err)
		}
		v := newDraftVersion(wf.ID, tenantID)
		vRepo := postgres.NewWorkflowVersionRepo(pool)
		if err := vRepo.Create(ctx, v); err != nil {
			t.Fatalf("create draft: %v", err)
		}
		if err := vRepo.Publish(ctx, tenantID, v.ID, 1, 1, `{}`, "h", refs); err != nil {
			t.Fatalf("publish: %v", err)
		}
		return v
	}
	both := []domain.ModuleRef{ref(check, checkVersions[0]), ref(review, reviewVersions[0]), ref(review, reviewVersions[1])}
	pinnedOld := publish(tenantA, "a-both", both)
	publish(tenantA, "b-check-only", []domain.ModuleRef{ref(check, checkVersions[0])})
	publish(tenantA, "c-none", nil)
	publish(tenantB, "d-other-tenant", []domain.ModuleRef{ref(review, reviewVersions[1])})

	got, err := postgres.NewWorkflowVersionRepo(pool).GetByID(ctx, tenantA, pinnedOld.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !reflect.DeepEqual(got.ModuleRefs, both) {
		t.Errorf("module refs = %+v, want %+v stored as given", got.ModuleRefs, both)
	}

	usagesRepo := postgres.NewWorkflowVersionRepo(fixtures.NewRestrictedRolePool(t, dsn, false, &tenantA, &userA))
	usages, err := usagesRepo.ListModuleUsages(ctx, tenantA, review.ID)
	if err != nil {
		t.Fatalf("ListModuleUsages: %v", err)
	}
	if len(usages) != 2 {
		t.Fatalf("usages = %+v, want the two review versions in a-both and nothing from tenant B", usages)
	}
	for i, want := range []int32{1, 2} {
		u := usages[i]
		if u.WorkflowKey != "a-both" || u.VersionID != pinnedOld.ID || u.ModuleVersionNumber != want ||
			u.Status != domain.VersionStatusPublished || u.VersionNumber == nil || *u.VersionNumber != 1 {
			t.Errorf("usage %d = %+v, want a-both using review v%d", i, u, want)
		}
	}

	none, err := usagesRepo.ListModuleUsages(ctx, tenantA, uuid.New())
	if err != nil || len(none) != 0 {
		t.Errorf("an unused module: usages = %+v, err = %v, want none", none, err)
	}
}

// A tenant module and a global one can share a process id only through a race
// between two creates; resolution then prefers the tenant's own.
func TestModuleVersionRepo_Resolve_PrefersTheTenantsOwnOnAProcessIDClash(t *testing.T) {
	pool := newTestPool(t)
	tenantID := uuid.New()
	libraryModule(t, pool, nil, "Process_Clash", 1)
	_, own := libraryModule(t, pool, &tenantID, "Process_Clash", 1)

	for range 5 {
		v, err := postgres.NewModuleVersionRepo(pool).Resolve(context.Background(), tenantID, "Process_Clash", 0)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if v.ID != own[0].ID {
			t.Fatalf("resolved %s (scope %s), want the tenant's own %s", v.ID, v.Scope, own[0].ID)
		}
	}
}

func callerTenant(tenantID *uuid.UUID) uuid.UUID {
	if tenantID != nil {
		return *tenantID
	}
	return uuid.New()
}
