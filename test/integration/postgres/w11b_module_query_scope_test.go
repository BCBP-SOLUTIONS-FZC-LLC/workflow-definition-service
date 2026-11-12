//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func w11bModuleRef(m *domain.Module, v *domain.ModuleVersion) domain.ModuleRef {
	return domain.ModuleRef{ModuleID: m.ID, VersionID: v.ID, ProcessID: m.ProcessID, VersionNumber: *v.VersionNumber}
}

func w11bPublishedWorkflowVersion(
	t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID, name string, refs ...domain.ModuleRef,
) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	wf := &domain.Workflow{ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(), TemplateKey: name, Name: name}
	require.NoError(t, postgres.NewWorkflowRepo(pool).Create(ctx, wf))
	v := newDraftVersion(wf.ID, tenantID)
	repo := postgres.NewWorkflowVersionRepo(pool)
	require.NoError(t, repo.Create(ctx, v))
	require.NoError(t, repo.Publish(ctx, tenantID, v.ID, 1, 1, `{}`, "h", refs))
	return v.ID
}

func w11bUsageVersionIDs(usages []*domain.ModuleUsage) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(usages))
	for _, u := range usages {
		ids = append(ids, u.VersionID)
	}
	return ids
}

func TestWorkflowVersionRepo_ListModuleUsages_GlobalModuleUsedByTwoTenants_ListsOnlyTheNamedTenantsVersions(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	shared, sharedVersions := libraryModule(t, superPool, nil, "Process_W11bShared", 1)
	ref := w11bModuleRef(shared, sharedVersions[0])

	cases := []struct {
		name       string
		restricted bool
		askAsOther bool
		wantOther  bool
		wantNone   bool
	}{
		{name: "row-level security pool, asked under the caller's id", restricted: true},
		{name: "unrestricted pool, asked under the caller's id so only the query filter hides the other tenant"},
		{name: "row-level security pool, asked under the other tenant's id so the policy alone hides it", restricted: true, askAsOther: true, wantNone: true},
		{name: "unrestricted pool, asked under the other tenant's id selects that tenant's versions", askAsOther: true, wantOther: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caller, other, user := uuid.New(), uuid.New(), uuid.New()
			callerIDs := []uuid.UUID{
				w11bPublishedWorkflowVersion(t, superPool, caller, "wf-a", ref),
				w11bPublishedWorkflowVersion(t, superPool, caller, "wf-b", ref),
			}
			otherIDs := []uuid.UUID{
				w11bPublishedWorkflowVersion(t, superPool, other, "wf-a", ref),
				w11bPublishedWorkflowVersion(t, superPool, other, "wf-b", ref),
			}
			pool := superPool
			if tc.restricted {
				pool = fixtures.NewRestrictedRolePool(t, dsn, false, &caller, &user)
			}
			askedAs, want := caller, callerIDs
			if tc.askAsOther {
				askedAs = other
			}
			if tc.wantOther {
				want = otherIDs
			}
			if tc.wantNone {
				want = []uuid.UUID{}
			}

			got, err := postgres.NewWorkflowVersionRepo(pool).ListModuleUsages(ctx, askedAs, shared.ID)

			require.NoError(t, err)
			assert.Equal(t, want, w11bUsageVersionIDs(got))
		})
	}
}

func TestWorkflowVersionRepo_ListModuleUsages_VersionHoldingTwoModules_ListsOnlyTheAskedModulesEntries(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	tenant, user := uuid.New(), uuid.New()
	moduleA, versionsA := libraryModule(t, superPool, nil, "Process_W11bA", 1)
	moduleB, versionsB := libraryModule(t, superPool, nil, "Process_W11bB", 3)
	both := w11bPublishedWorkflowVersion(t, superPool, tenant, "wf-both",
		w11bModuleRef(moduleB, versionsB[2]), w11bModuleRef(moduleA, versionsA[0]), w11bModuleRef(moduleB, versionsB[1]))
	onlyB := w11bPublishedWorkflowVersion(t, superPool, tenant, "wf-only-b", w11bModuleRef(moduleB, versionsB[2]))
	repo := postgres.NewWorkflowVersionRepo(fixtures.NewRestrictedRolePool(t, dsn, false, &tenant, &user))

	cases := []struct {
		name         string
		module       uuid.UUID
		wantVersions []uuid.UUID
		wantNumbers  []int32
	}{
		{name: "module A", module: moduleA.ID, wantVersions: []uuid.UUID{both}, wantNumbers: []int32{1}},
		{name: "module B lists both of its entries of the shared version and the other workflow", module: moduleB.ID,
			wantVersions: []uuid.UUID{both, both, onlyB}, wantNumbers: []int32{2, 3, 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.ListModuleUsages(ctx, tenant, tc.module)

			require.NoError(t, err)
			numbers := make([]int32, 0, len(got))
			for _, u := range got {
				numbers = append(numbers, u.ModuleVersionNumber)
			}
			assert.Equal(t, tc.wantVersions, w11bUsageVersionIDs(got))
			assert.Equal(t, tc.wantNumbers, numbers)
		})
	}
}

func TestModuleService_GetVersion_GlobalVersion_IsReadByEveryTenantThroughTheRealQuery(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	module, versions := libraryModule(t, superPool, nil, "Process_W11bGlobalRead", 1)
	other, otherVersions := libraryModule(t, superPool, nil, "Process_W11bGlobalOther", 1)

	for _, name := range []string{"first tenant", "second tenant"} {
		t.Run(name, func(t *testing.T) {
			tenant, user := uuid.New(), uuid.New()
			svc := sgDModuleService(fixtures.NewRestrictedRolePool(t, dsn, false, &tenant, &user))

			got, err := svc.GetVersion(ctx, tenant, module.ID, versions[0].ID)

			require.NoError(t, err)
			assert.Equal(t, versions[0].ID, got.ID)
			assert.Equal(t, module.ID, got.ModuleID)
			assert.Equal(t, domain.ScopeGlobal, got.Scope)
			assert.Nil(t, got.TenantID)
			assert.Equal(t, domain.VersionStatusPublished, got.Status)

			_, err = svc.GetVersion(ctx, tenant, module.ID, otherVersions[0].ID)
			assert.ErrorIs(t, err, domain.ErrNotFound, "a version of another module under this module's id")
			_, err = svc.GetVersion(ctx, tenant, other.ID, otherVersions[0].ID)
			assert.NoError(t, err)
		})
	}
}

func TestModuleService_GetVersion_OtherTenantsVersion_IsNotFoundThroughTheRealQuery(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()

	cases := []struct {
		name       string
		restricted bool
		askAsOwner bool
	}{
		{name: "unrestricted pool, caller's id so only the query filter hides it"},
		{name: "row-level security pool, caller's id", restricted: true},
		{name: "row-level security pool, owner's id so the policy alone hides it", restricted: true, askAsOwner: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner, caller, user := uuid.New(), uuid.New(), uuid.New()
			module, versions := libraryModule(t, superPool, &owner, "Process_W11bOwned_"+uuid.NewString(), 1)
			pool := superPool
			if tc.restricted {
				pool = fixtures.NewRestrictedRolePool(t, dsn, false, &caller, &user)
			}
			askedAs := caller
			if tc.askAsOwner {
				askedAs = owner
			}

			got, err := sgDModuleService(pool).GetVersion(ctx, askedAs, module.ID, versions[0].ID)

			assert.ErrorIs(t, err, domain.ErrNotFound)
			assert.Nil(t, got)
			ownerView, err := sgDModuleService(superPool).GetVersion(ctx, owner, module.ID, versions[0].ID)
			require.NoError(t, err, "control: the owner reads the version")
			assert.Equal(t, owner, *ownerView.TenantID)
		})
	}
}

func TestModuleVersionRepo_GetByID_ScopeOwnerAndArchivedThroughRLS(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	_, globalVersions := libraryModule(t, superPool, nil, "Process_W11bGlobalGet", 1)

	for _, name := range []string{"first tenant", "second tenant"} {
		t.Run("global version read by the "+name, func(t *testing.T) {
			tenant, user := uuid.New(), uuid.New()
			repo := postgres.NewModuleVersionRepo(fixtures.NewRestrictedRolePool(t, dsn, false, &tenant, &user))

			got, err := repo.GetByID(ctx, tenant, globalVersions[0].ID)

			require.NoError(t, err)
			assert.Equal(t, globalVersions[0].ID, got.ID)
			assert.Equal(t, domain.ScopeGlobal, got.Scope)
			assert.Nil(t, got.TenantID)
		})
	}

	t.Run("another tenant's version is hidden under either id while its owner reads it", func(t *testing.T) {
		owner, caller, ownerUser, callerUser := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		_, versions := libraryModule(t, superPool, &owner, "Process_W11bOwnedGet", 1)
		callerRepo := postgres.NewModuleVersionRepo(fixtures.NewRestrictedRolePool(t, dsn, false, &caller, &callerUser))
		ownerRepo := postgres.NewModuleVersionRepo(fixtures.NewRestrictedRolePool(t, dsn, false, &owner, &ownerUser))

		for _, askedAs := range []uuid.UUID{caller, owner} {
			got, err := callerRepo.GetByID(ctx, askedAs, versions[0].ID)
			assert.ErrorIs(t, err, domain.ErrNotFound)
			assert.Nil(t, got)
		}
		got, err := ownerRepo.GetByID(ctx, owner, versions[0].ID)
		require.NoError(t, err)
		assert.Equal(t, versions[0].ID, got.ID)
		require.NotNil(t, got.TenantID)
		assert.Equal(t, owner, *got.TenantID)
	})

	t.Run("an archived version is still returned with the number and publication time it had", func(t *testing.T) {
		tenant, user := uuid.New(), uuid.New()
		_, versions := libraryModule(t, superPool, &tenant, "Process_W11bArchivedGet", 1)
		published, err := postgres.NewModuleVersionRepo(superPool).GetByID(ctx, tenant, versions[0].ID)
		require.NoError(t, err)
		require.NotNil(t, published.PublishedAt)
		require.NoError(t, postgres.NewModuleVersionRepo(superPool).Archive(ctx, tenant, versions[0].ID))
		repo := postgres.NewModuleVersionRepo(fixtures.NewRestrictedRolePool(t, dsn, false, &tenant, &user))

		got, err := repo.GetByID(ctx, tenant, versions[0].ID)

		require.NoError(t, err)
		assert.Equal(t, domain.VersionStatusArchived, got.Status)
		require.NotNil(t, got.VersionNumber)
		assert.EqualValues(t, 1, *got.VersionNumber)
		require.NotNil(t, got.PublishedAt)
		assert.True(t, got.PublishedAt.Equal(*published.PublishedAt), "published_at %v changed from %v on archive", got.PublishedAt, published.PublishedAt)
	})
}

func TestModuleVersionRepo_ListByModule_OtherTenantsVersions_AreHiddenWhileTheOwnerListsThem(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()

	cases := []struct {
		name       string
		restricted bool
		askAsOwner bool
	}{
		{name: "unrestricted pool, caller's id so only the query filter hides them"},
		{name: "row-level security pool, caller's id", restricted: true},
		{name: "row-level security pool, owner's id so the policy alone hides them", restricted: true, askAsOwner: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner, caller, ownerUser, callerUser := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			mod := newTestModule(t, ctx, superPool, owner)
			ids := moduleVersionListInsertArchived(t, superPool, mod.ID, &owner, domain.ScopeTenant, 2)
			pool := superPool
			if tc.restricted {
				pool = fixtures.NewRestrictedRolePool(t, dsn, false, &caller, &callerUser)
			}
			askedAs := caller
			if tc.askAsOwner {
				askedAs = owner
			}

			got, total, err := postgres.NewModuleVersionRepo(pool).ListByModule(ctx, askedAs, mod.ID, 1, 20)

			require.NoError(t, err)
			assert.Zero(t, total)
			assert.Empty(t, got)

			ownerGot, ownerTotal, err := postgres.NewModuleVersionRepo(
				fixtures.NewRestrictedRolePool(t, dsn, false, &owner, &ownerUser),
			).ListByModule(ctx, owner, mod.ID, 1, 20)
			require.NoError(t, err, "control: the owner lists the versions")
			assert.EqualValues(t, 2, ownerTotal)
			assert.ElementsMatch(t, ids, sgDVersionIDs(ownerGot))
		})
	}
}
