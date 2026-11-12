//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func w4aNewWorkflow(tenantID uuid.UUID, key string) *domain.Workflow {
	return &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(),
		TemplateKey: key, Name: "Name " + key, Description: "Description " + key,
	}
}

func w4aFailInTx(t *testing.T, pool *pgcommon.Pool, call func(ctx context.Context) error) error {
	t.Helper()
	var got error
	_ = pgadapter.NewTransactor(pool).RunInTx(context.Background(), func(txCtx context.Context) error {
		expired, cancel := context.WithDeadline(txCtx, time.Now().Add(-time.Second))
		defer cancel()
		got = call(expired)
		return got
	})
	return got
}

func TestRepositories_InsideATransactionUseItAndOutsideOneUseThePool(t *testing.T) {
	pool := newTestPool(t)
	repo := pgadapter.NewWorkflowRepo(pool)
	transactor := pgadapter.NewTransactor(pool)
	ctx := context.Background()
	tenantID := uuid.New()
	inTx, viaPool, committed := w4aNewWorkflow(tenantID, "in-tx"), w4aNewWorkflow(tenantID, "via-pool"), w4aNewWorkflow(tenantID, "committed")
	rollback := errors.New("roll the transaction back")

	err := transactor.RunInTx(ctx, func(txCtx context.Context) error {
		require.NoError(t, repo.Create(txCtx, inTx))
		require.NoError(t, repo.Create(ctx, viaPool))

		seen, err := repo.GetByID(txCtx, tenantID, inTx.ID)
		require.NoError(t, err)
		assert.Equal(t, inTx.ID, seen.ID, "a read with the transaction context sees the transaction's own uncommitted write")
		_, err = repo.GetByID(ctx, tenantID, inTx.ID)
		assert.ErrorIs(t, err, domain.ErrNotFound, "a read without it runs on another connection and cannot see the uncommitted row")
		return rollback
	})
	require.ErrorIs(t, err, rollback)

	_, err = repo.GetByID(ctx, tenantID, inTx.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound, "the write made with the transaction context was rolled back")
	kept, err := repo.GetByID(ctx, tenantID, viaPool.ID)
	require.NoError(t, err, "the write made without it was committed on its own connection and survives the rollback")
	assert.Equal(t, viaPool.TemplateKey, kept.TemplateKey)

	require.NoError(t, transactor.RunInTx(ctx, func(txCtx context.Context) error {
		require.NoError(t, repo.Create(txCtx, committed))
		_, err := repo.GetByID(ctx, tenantID, committed.ID)
		assert.ErrorIs(t, err, domain.ErrNotFound, "uncommitted writes stay private until the commit")
		return nil
	}))
	got, err := repo.GetByID(ctx, tenantID, committed.ID)
	require.NoError(t, err)
	assert.Equal(t, committed.ID, got.ID)
}

func TestRepositories_GetByID_ReturnsTheMappedEntityOrNotFoundOrTheClassifiedQueryError(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	workflows, versions := pgadapter.NewWorkflowRepo(pool), pgadapter.NewWorkflowVersionRepo(pool)
	modules, moduleVersions := pgadapter.NewModuleRepo(pool), pgadapter.NewModuleVersionRepo(pool)
	tenantID, otherTenant := uuid.New(), uuid.New()

	wf := w4aNewWorkflow(tenantID, "get-by-id")
	require.NoError(t, workflows.Create(ctx, wf))
	draft := newDraftVersion(wf.ID, tenantID)
	require.NoError(t, versions.Create(ctx, draft))
	moduleUser := uuid.New()
	mod := &domain.Module{ID: uuid.New(), TenantID: &tenantID, Scope: domain.ScopeTenant, Name: "Module", Description: "Module description", ProcessID: "Process_" + uuid.NewString(), CreatedByUserID: &moduleUser}
	require.NoError(t, modules.Create(ctx, mod))
	modVersion := newDraftModuleVersion(mod.ID, &tenantID, domain.ScopeTenant)
	modVersion.ProcessID = mod.ProcessID
	require.NoError(t, moduleVersions.Create(ctx, modVersion))
	require.NoError(t, versions.Publish(ctx, tenantID, draft.ID, 1, 3, `{}`, "h", nil))
	require.NoError(t, moduleVersions.Publish(ctx, tenantID, modVersion.ID, 1))

	t.Run("workflow before a version becomes active", func(t *testing.T) {
		before, err := workflows.GetByID(ctx, tenantID, wf.ID)
		require.NoError(t, err)
		assert.Equal(t, wf.ID, before.ID)
		assert.Equal(t, tenantID, before.TenantID)
		assert.Equal(t, wf.TemplateKey, before.TemplateKey)
		assert.Equal(t, wf.Name, before.Name)
		assert.Equal(t, wf.Description, before.Description)
		assert.Equal(t, wf.CreatedByUserID, before.CreatedByUserID)
		assert.Nil(t, before.ActiveVersionID)
		assert.Nil(t, before.ActiveVersionNumber, "no active version means no number")
	})

	t.Run("workflow after a version becomes active", func(t *testing.T) {
		require.NoError(t, workflows.UpdateActiveVersion(ctx, tenantID, wf.ID, &draft.ID))

		after, err := workflows.GetByID(ctx, tenantID, wf.ID)
		require.NoError(t, err)
		require.NotNil(t, after.ActiveVersionID)
		assert.Equal(t, draft.ID, *after.ActiveVersionID)
		require.NotNil(t, after.ActiveVersionNumber, "the active version's number is filled in")
		assert.EqualValues(t, 3, *after.ActiveVersionNumber)
	})

	t.Run("workflow version", func(t *testing.T) {
		got, err := versions.GetByID(ctx, tenantID, draft.ID)
		require.NoError(t, err)
		assert.Equal(t, draft.ID, got.ID)
		assert.Equal(t, wf.ID, got.WorkflowID)
		assert.Equal(t, tenantID, got.TenantID)
		assert.Equal(t, domain.VersionStatusPublished, got.Status)
		assert.Equal(t, draft.BPMNXML, got.BPMNXML)
		require.NotNil(t, got.VersionNumber)
		assert.EqualValues(t, 3, *got.VersionNumber)
		assert.NotNil(t, got.PublishedAt)
		assert.Equal(t, draft.CreatedByUserID, got.CreatedByUserID)
	})

	t.Run("module", func(t *testing.T) {
		require.NoError(t, modules.UpdateActiveVersion(ctx, mod.ID, &modVersion.ID))

		got, err := modules.GetByID(ctx, tenantID, mod.ID)
		require.NoError(t, err)
		assert.Equal(t, mod.ID, got.ID)
		require.NotNil(t, got.TenantID)
		assert.Equal(t, tenantID, *got.TenantID)
		assert.Equal(t, domain.ScopeTenant, got.Scope)
		assert.Equal(t, mod.Name, got.Name)
		assert.Equal(t, mod.Description, got.Description)
		assert.Equal(t, mod.ProcessID, got.ProcessID)
		require.NotNil(t, got.ActiveVersionID)
		assert.Equal(t, modVersion.ID, *got.ActiveVersionID)
		require.NotNil(t, got.CreatedByUserID)
		assert.Equal(t, moduleUser, *got.CreatedByUserID)
	})

	t.Run("module version", func(t *testing.T) {
		got, err := moduleVersions.GetByID(ctx, tenantID, modVersion.ID)
		require.NoError(t, err)
		assert.Equal(t, modVersion.ID, got.ID)
		assert.Equal(t, mod.ID, got.ModuleID)
		assert.Equal(t, domain.ScopeTenant, got.Scope)
		assert.Equal(t, domain.VersionStatusPublished, got.Status)
		assert.Equal(t, mod.ProcessID, got.ProcessID)
		assert.Equal(t, modVersion.BPMNXML, got.BPMNXML)
		require.NotNil(t, got.VersionNumber)
		assert.EqualValues(t, 1, *got.VersionNumber)
	})

	lookups := []struct {
		name string
		id   uuid.UUID
		get  func(ctx context.Context, tenant, id uuid.UUID) error
	}{
		{"workflow", wf.ID, func(ctx context.Context, tenant, id uuid.UUID) error {
			_, err := workflows.GetByID(ctx, tenant, id)
			return err
		}},
		{"workflow version", draft.ID, func(ctx context.Context, tenant, id uuid.UUID) error {
			_, err := versions.GetByID(ctx, tenant, id)
			return err
		}},
		{"module", mod.ID, func(ctx context.Context, tenant, id uuid.UUID) error {
			_, err := modules.GetByID(ctx, tenant, id)
			return err
		}},
		{"module version", modVersion.ID, func(ctx context.Context, tenant, id uuid.UUID) error {
			_, err := moduleVersions.GetByID(ctx, tenant, id)
			return err
		}},
	}
	for _, tc := range lookups {
		t.Run(tc.name+" unknown id and another tenant's id are not found", func(t *testing.T) {
			assert.ErrorIs(t, tc.get(ctx, tenantID, uuid.New()), domain.ErrNotFound)
			assert.ErrorIs(t, tc.get(ctx, otherTenant, tc.id), domain.ErrNotFound)
		})
		t.Run(tc.name+" query failure is an outage and not a missing row", func(t *testing.T) {
			err := w4aFailInTx(t, pool, func(ctx context.Context) error { return tc.get(ctx, tenantID, tc.id) })

			require.Error(t, err)
			assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.False(t, errors.Is(err, domain.ErrNotFound), "an outage must not read as not found: %v", err)
		})
	}
}

func TestWorkflowRepo_GetByTemplateKey_UnknownKeyOtherTenantsKeyOrUnreachableDatabase(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	owner, caller, callerUser := uuid.New(), uuid.New(), uuid.New()
	wf := w4aNewWorkflow(owner, "owners-key")
	require.NoError(t, pgadapter.NewWorkflowRepo(superPool).Create(ctx, wf))
	callerRepo := pgadapter.NewWorkflowRepo(fixtures.NewRestrictedRolePool(t, dsn, false, &caller, &callerUser))
	closedPool, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: dsn})
	require.NoError(t, err)
	closedPool.Close()

	got, err := pgadapter.NewWorkflowRepo(superPool).GetByTemplateKey(ctx, owner, "owners-key")
	require.NoError(t, err)
	assert.Equal(t, wf.ID, got.ID)
	assert.Equal(t, owner, got.TenantID)
	assert.Equal(t, wf.Name, got.Name)

	cases := []struct {
		name    string
		repo    *pgadapter.WorkflowRepo
		tenant  uuid.UUID
		key     string
		wantErr error
	}{
		{"unknown key", pgadapter.NewWorkflowRepo(superPool), owner, "no-such-key", domain.ErrNotFound},
		{"key owned by another tenant, asked as the caller", callerRepo, caller, "owners-key", domain.ErrNotFound},
		{"key owned by another tenant, asked under the owner's id through the caller's row-level security", callerRepo, owner, "owners-key", domain.ErrNotFound},
		{"closed pool", pgadapter.NewWorkflowRepo(closedPool), owner, "owners-key", domain.ErrUpstreamUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.repo.GetByTemplateKey(ctx, tc.tenant, tc.key)

			require.ErrorIs(t, err, tc.wantErr)
			assert.Nil(t, got)
			if tc.wantErr == domain.ErrUpstreamUnavailable {
				assert.False(t, errors.Is(err, domain.ErrNotFound), "an outage must not read as not found: %v", err)
			}
		})
	}
}
