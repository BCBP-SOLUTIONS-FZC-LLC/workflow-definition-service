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

func wireStarterService(tenantPool, systemPool *pgcommon.Pool) *service.StarterService {
	return service.NewStarterService(service.StarterDeps{
		Starters:       pgadapter.NewStarterRepo(tenantPool),
		GlobalStarters: pgadapter.NewStarterRepo(systemPool),
		Versions:       pgadapter.NewWorkflowVersionRepo(tenantPool),
		Compiler:       bpmn_compiler.New(),
	})
}

func TestE2E_StarterLifecycle(t *testing.T) {
	ctx := context.Background()
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantID, userID := uuid.New(), uuid.New()
	tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)

	starterSvc := wireStarterService(tenantPool, systemPool)

	// Tenant-scoped starter created directly from raw XML.
	st, err := starterSvc.Create(ctx, tenantID, userID, service.CreateStarterReq{
		Name: "Direct Starter", Category: "ops", BPMNXML: minimalBPMN,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if st.Scope != domain.ScopeTenant || st.TenantID == nil || *st.TenantID != tenantID {
		t.Fatalf("unexpected starter: %+v", st)
	}

	got, err := starterSvc.Get(ctx, tenantID, st.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BPMNXML != minimalBPMN {
		t.Errorf("BPMNXML mismatch: got %d bytes, want %d bytes", len(got.BPMNXML), len(minimalBPMN))
	}

	if err := starterSvc.Delete(ctx, tenantID, st.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := starterSvc.Get(ctx, tenantID, st.ID); err == nil {
		t.Error("expected error getting a deleted starter")
	}
}

func TestE2E_StarterGlobalScope_VisibleAcrossTenants(t *testing.T) {
	ctx := context.Background()
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantID, userID := uuid.New(), uuid.New()
	tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)
	starterSvc := wireStarterService(tenantPool, systemPool)

	st, err := starterSvc.Create(ctx, tenantID, userID, service.CreateStarterReq{
		Name: "Global Starter", BPMNXML: minimalBPMN, Scope: domain.ScopeGlobal,
	})
	if err != nil {
		t.Fatalf("Create(global): %v", err)
	}

	got, err := starterSvc.Get(ctx, uuid.New(), st.ID)
	if err != nil {
		t.Fatalf("Get(global) from an unrelated tenant: %v", err)
	}
	if got.Scope != domain.ScopeGlobal {
		t.Errorf("expected scope=global, got %v", got.Scope)
	}
}

// TestE2E_StarterFromWorkflowVersion_SnapshotsNotLinks proves the LLD's "copied,
// not linked" requirement for real: mutating the source workflow's content
// after the starter is created must not change the starter's own BPMN XML.
func TestE2E_StarterFromWorkflowVersion_SnapshotsNotLinks(t *testing.T) {
	ctx := context.Background()
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantID, userID := uuid.New(), uuid.New()
	tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)

	svcs := wireServices(t, tenantPool, nil)
	starterSvc := wireStarterService(tenantPool, systemPool)

	wf, draft, err := svcs.workflow.Create(ctx, tenantID, userID, "source-wf", "Source Workflow", "", minimalBPMN, "")
	if err != nil {
		t.Fatalf("Create workflow: %v", err)
	}
	published, err := svcs.version.Publish(ctx, tenantID, userID, wf.ID, draft.ID, false)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	st, err := starterSvc.CreateFromWorkflowVersion(ctx, tenantID, userID, published.ID, service.CreateFromWorkflowVersionReq{
		Name: "Snapshot Starter",
	})
	if err != nil {
		t.Fatalf("CreateFromWorkflowVersion: %v", err)
	}
	if st.Scope != domain.ScopeTenant {
		t.Fatalf("expected scope=tenant, got %v", st.Scope)
	}
	if st.SourceWorkflowVersionID == nil || *st.SourceWorkflowVersionID != published.ID {
		t.Fatalf("expected source_workflow_version_id=%v, got %v", published.ID, st.SourceWorkflowVersionID)
	}
	originalXML := st.BPMNXML

	// Mutate the source: a new draft with different content, published as v2.
	if _, err := svcs.draft.Init(ctx, tenantID, userID, wf.ID); err != nil {
		t.Fatalf("Init second draft: %v", err)
	}
	mutatedXML := minimalBPMN + "<!-- mutated after snapshot -->"
	draft2, err := svcs.draft.Get(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("Get second draft: %v", err)
	}
	if _, err := svcs.draft.Update(ctx, tenantID, userID, wf.ID, service.UpdateDraftReq{BPMNXML: &mutatedXML}); err != nil {
		t.Fatalf("Update second draft: %v", err)
	}
	if _, err := svcs.version.Publish(ctx, tenantID, userID, wf.ID, draft2.ID, false); err != nil {
		t.Fatalf("Publish second draft: %v", err)
	}

	// The already-created starter must be unaffected by the source's later mutation.
	after, err := starterSvc.Get(ctx, tenantID, st.ID)
	if err != nil {
		t.Fatalf("Get starter after source mutation: %v", err)
	}
	if after.BPMNXML != originalXML {
		t.Errorf("starter BPMN XML changed after source workflow was mutated — snapshot leaked a live reference")
	}
}

func TestE2E_StarterFromWorkflowVersion_RejectsDraftSource(t *testing.T) {
	ctx := context.Background()
	_, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantID, userID := uuid.New(), uuid.New()
	tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)

	svcs := wireServices(t, tenantPool, nil)
	starterSvc := wireStarterService(tenantPool, systemPool)

	_, draft, err := svcs.workflow.Create(ctx, tenantID, userID, "draft-source-wf", "Draft Source", "", minimalBPMN, "")
	if err != nil {
		t.Fatalf("Create workflow: %v", err)
	}

	_, err = starterSvc.CreateFromWorkflowVersion(ctx, tenantID, userID, draft.ID, service.CreateFromWorkflowVersionReq{Name: "s"})
	if err != domain.ErrVersionNotPublished {
		t.Errorf("CreateFromWorkflowVersion(draft source) = %v, want ErrVersionNotPublished", err)
	}
}
