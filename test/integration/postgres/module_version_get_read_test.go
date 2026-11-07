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

func TestModuleVersionRepo_GetByID_VisibilityByScopeTenantAndStatus(t *testing.T) {
	pool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	tenantA, tenantB, userA := uuid.New(), uuid.New(), uuid.New()

	_, globalVersions := libraryModule(t, pool, nil, "Process_Global_"+uuid.NewString(), 1)
	_, ownVersions := libraryModule(t, pool, &tenantA, "Process_Own_"+uuid.NewString(), 1)
	_, otherVersions := libraryModule(t, pool, &tenantB, "Process_Other_"+uuid.NewString(), 1)
	_, archivedVersions := libraryModule(t, pool, &tenantA, "Process_Archived_"+uuid.NewString(), 1)
	if err := postgres.NewModuleVersionRepo(pool).Archive(ctx, tenantA, archivedVersions[0].ID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	tenantARepo := postgres.NewModuleVersionRepo(fixtures.NewRestrictedRolePool(t, dsn, false, &tenantA, &userA))

	readable := []struct {
		name       string
		versionID  uuid.UUID
		wantModule uuid.UUID
		wantScope  domain.CatalogScope
		wantStatus domain.VersionStatus
	}{
		{"global version read by a tenant", globalVersions[0].ID, globalVersions[0].ModuleID, domain.ScopeGlobal, domain.VersionStatusPublished},
		{"own tenant version", ownVersions[0].ID, ownVersions[0].ModuleID, domain.ScopeTenant, domain.VersionStatusPublished},
		{"archived version", archivedVersions[0].ID, archivedVersions[0].ModuleID, domain.ScopeTenant, domain.VersionStatusArchived},
	}
	for _, tc := range readable {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tenantARepo.GetByID(ctx, tenantA, tc.versionID)
			if err != nil {
				t.Fatalf("GetByID = %v", err)
			}
			if got.ID != tc.versionID || got.ModuleID != tc.wantModule || got.Scope != tc.wantScope || got.Status != tc.wantStatus {
				t.Errorf("GetByID = %+v, want module %s scope %s status %s", got, tc.wantModule, tc.wantScope, tc.wantStatus)
			}
			if got.VersionNumber == nil || *got.VersionNumber != 1 || got.PublishedAt == nil {
				t.Errorf("version_number/published_at = %v/%v, want 1 and a publication time", got.VersionNumber, got.PublishedAt)
			}
		})
	}

	hidden := []struct {
		name     string
		callerID uuid.UUID
	}{
		{"another tenant's version asked as the owning tenant id", tenantB},
		{"another tenant's version asked as the caller's tenant id", tenantA},
	}
	for _, tc := range hidden {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tenantARepo.GetByID(ctx, tc.callerID, otherVersions[0].ID)
			if !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("GetByID = %v, want ErrNotFound", err)
			}
		})
	}
}
