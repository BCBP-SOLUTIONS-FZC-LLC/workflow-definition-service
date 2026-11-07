package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func TestModuleRepo_GetByID_OtherTenantsModuleIsNotFoundThroughRLS(t *testing.T) {
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantA, tenantB, userA := uuid.New(), uuid.New(), uuid.New()
	ownID, otherID := uuid.New(), uuid.New()
	insertModule(t, dsn, ownID, "tenant", &tenantA)
	insertModule(t, dsn, otherID, "tenant", &tenantB)
	repo := postgres.NewModuleRepo(fixtures.NewRestrictedRolePool(t, dsn, false, &tenantA, &userA))

	cases := []struct {
		name     string
		callerID uuid.UUID
		moduleID uuid.UUID
		wantErr  error
	}{
		{name: "own module as the caller", callerID: tenantA, moduleID: ownID},
		{name: "other tenant's module as the caller", callerID: tenantA, moduleID: otherID, wantErr: domain.ErrNotFound},
		{name: "other tenant's module asked for under that tenant's id", callerID: tenantB, moduleID: otherID, wantErr: domain.ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.GetByID(context.Background(), tc.callerID, tc.moduleID)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("GetByID = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && (got == nil || got.ID != tc.moduleID) {
				t.Errorf("module = %+v, want %s", got, tc.moduleID)
			}
			if tc.wantErr != nil && got != nil {
				t.Errorf("module = %+v, want none", got)
			}
		})
	}
}

func TestWorkflowVersionRepo_ListModuleUsages_ClosedPoolIsUnavailableNotEmpty(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowVersionRepo(pool)
	pool.Close()

	usages, err := repo.ListModuleUsages(context.Background(), uuid.New(), uuid.New())

	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Errorf("ListModuleUsages on a closed pool = %v, want ErrUpstreamUnavailable so the API answers 503", err)
	}
	if errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a closed pool must not be reported as not found: %v", err)
	}
	if usages != nil {
		t.Errorf("usages = %+v, want none on failure", usages)
	}
}

func TestWorkflowVersionRepo_ListModuleUsages_OtherTenantsReferencesAreHiddenByRLS(t *testing.T) {
	pool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	tenantA, tenantB, userA := uuid.New(), uuid.New(), uuid.New()
	module, versions := libraryModule(t, pool, nil, "Process_Shared", 1)
	ref := domain.ModuleRef{
		ModuleID: module.ID, VersionID: versions[0].ID, ProcessID: module.ProcessID,
		VersionNumber: *versions[0].VersionNumber,
	}
	for _, tenantID := range []uuid.UUID{tenantA, tenantB} {
		wf := &domain.Workflow{
			ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(),
			TemplateKey: "uses-shared", Name: "uses-shared",
		}
		if err := postgres.NewWorkflowRepo(pool).Create(ctx, wf); err != nil {
			t.Fatalf("create workflow: %v", err)
		}
		v := newDraftVersion(wf.ID, tenantID)
		vRepo := postgres.NewWorkflowVersionRepo(pool)
		if err := vRepo.Create(ctx, v); err != nil {
			t.Fatalf("create draft: %v", err)
		}
		if err := vRepo.Publish(ctx, tenantID, v.ID, 1, 1, `{}`, "h", []domain.ModuleRef{ref}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	repo := postgres.NewWorkflowVersionRepo(fixtures.NewRestrictedRolePool(t, dsn, false, &tenantA, &userA))

	cases := []struct {
		name      string
		callerID  uuid.UUID
		wantCount int
	}{
		{name: "asked under the caller's own tenant id", callerID: tenantA, wantCount: 1},
		{name: "asked under the other tenant's id on the caller's pool", callerID: tenantB, wantCount: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			usages, err := repo.ListModuleUsages(ctx, tc.callerID, module.ID)

			if err != nil {
				t.Fatalf("ListModuleUsages: %v", err)
			}
			if len(usages) != tc.wantCount {
				t.Errorf("usages = %+v, want %d", usages, tc.wantCount)
			}
		})
	}
}
