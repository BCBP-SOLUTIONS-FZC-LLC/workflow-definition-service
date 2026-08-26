//go:build integration

package e2e_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func wireModuleService(tenantPool, systemPool *pgcommon.Pool) *service.ModuleService {
	compiler := bpmn_compiler.New()
	return service.NewModuleService(service.ModuleDeps{
		Transactor:       pgadapter.NewTransactor(tenantPool),
		GlobalTransactor: pgadapter.NewTransactor(systemPool),
		Modules:          pgadapter.NewModuleRepo(tenantPool),
		GlobalModules:    pgadapter.NewModuleRepo(systemPool),
		Versions:         pgadapter.NewModuleVersionRepo(tenantPool),
		GlobalVersions:   pgadapter.NewModuleVersionRepo(systemPool),
		Compiler:         compiler,
	})
}

// TestE2E_ModuleLifecycle exercises the full tenant-scoped module state
// machine through the real service+repo layers against a real, genuinely
// RLS-restricted (non-superuser) Postgres role — not the table-owner pool
// every other e2e test in this package uses.
func TestE2E_ModuleLifecycle(t *testing.T) {
	ctx := context.Background()
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantID, userID := uuid.New(), uuid.New()
	tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)
	svc := wireModuleService(tenantPool, systemPool)

	mod, draft, err := svc.Create(ctx, tenantID, userID, service.CreateModuleReq{
		Name: "Approval Chain", BPMNXML: minimalBPMN,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if draft.Status != domain.VersionStatusDraft || draft.ProcessID != "P1" {
		t.Fatalf("unexpected draft: %+v", draft)
	}

	published, err := svc.Publish(ctx, tenantID, userID, mod.ID, draft.ID)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if published.Status != domain.VersionStatusPublished || published.VersionNumber == nil || *published.VersionNumber != 1 {
		t.Fatalf("unexpected published version: %+v", published)
	}

	got, active, err := svc.Get(ctx, tenantID, mod.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ActiveVersionID == nil || *got.ActiveVersionID != published.ID {
		t.Fatalf("expected active_version_id=%v, got %v", published.ID, got.ActiveVersionID)
	}
	if active == nil || active.ID != published.ID {
		t.Fatalf("expected active version %v, got %+v", published.ID, active)
	}

	if err := svc.Archive(ctx, tenantID, userID, mod.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	gotAfter, _, err := svc.Get(ctx, tenantID, mod.ID)
	if err != nil {
		t.Fatalf("Get after archive: %v", err)
	}
	if gotAfter.ActiveVersionID != nil {
		t.Errorf("expected active_version_id=nil after archive, got %v", *gotAfter.ActiveVersionID)
	}
}

// TestE2E_ModuleGlobalScope_RequiresRealBypassRLSPool proves the BYPASSRLS
// wiring end-to-end: a scope=global write only succeeds when the service's
// "global" pool is genuinely BYPASSRLS. Wiring an ordinary role into that slot
// — e.g. the exact kind of bug a Transactor/repo pool mismatch would cause —
// must fail at the database, not silently write through the wrong connection.
func TestE2E_ModuleGlobalScope_RequiresRealBypassRLSPool(t *testing.T) {
	ctx := context.Background()
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantID, userID := uuid.New(), uuid.New()

	t.Run("succeeds with a real BYPASSRLS pool", func(t *testing.T) {
		tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
		systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)
		svc := wireModuleService(tenantPool, systemPool)

		mod, _, err := svc.Create(ctx, tenantID, userID, service.CreateModuleReq{
			Name: "Global Module", BPMNXML: minimalBPMN, Scope: domain.ScopeGlobal,
		})
		if err != nil {
			t.Fatalf("Create(global): %v", err)
		}
		if mod.Scope != domain.ScopeGlobal || mod.TenantID != nil {
			t.Errorf("unexpected module: %+v", mod)
		}

		// Any tenant's own pool can read the global row back (RLS allows it).
		got, _, err := svc.Get(ctx, uuid.New(), mod.ID)
		if err != nil {
			t.Fatalf("Get(global) from an unrelated tenant: %v", err)
		}
		if got.Scope != domain.ScopeGlobal {
			t.Errorf("expected scope=global, got %v", got.Scope)
		}
	})

	t.Run("fails when the global slot is an ordinary role", func(t *testing.T) {
		tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
		notActuallyBypassRLS := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
		svc := wireModuleService(tenantPool, notActuallyBypassRLS)

		_, _, err := svc.Create(ctx, tenantID, userID, service.CreateModuleReq{
			Name: "Should Not Be Written", BPMNXML: minimalBPMN, Scope: domain.ScopeGlobal,
		})
		if err == nil {
			t.Fatal("expected the database to reject a global-scope write through an ordinary role, got no error")
		}
	})
}
