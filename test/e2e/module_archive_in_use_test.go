//go:build integration

package e2e_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

const callsP1BPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="D2" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Caller" name="Caller" isExecutable="true">
    <bpmn:callActivity id="Call_1" name="Call library module">
      <bpmn:extensionElements><zeebe:calledElement processId="P1" propagateAllChildVariables="false"/></bpmn:extensionElements>
    </bpmn:callActivity>
  </bpmn:process>
</bpmn:definitions>`

func seedCallerWorkflow(t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID, key, xml string) *domain.WorkflowVersion {
	t.Helper()
	ctx := context.Background()
	wf := &domain.Workflow{ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(), TemplateKey: key, Name: key}
	if err := pgadapter.NewWorkflowRepo(pool).Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	v := &domain.WorkflowVersion{
		ID: uuid.New(), WorkflowID: wf.ID, TenantID: tenantID, CreatedByUserID: uuid.New(),
		Status: domain.VersionStatusDraft, BPMNXML: xml, IsValid: true,
	}
	if err := pgadapter.NewWorkflowVersionRepo(pool).Create(ctx, v); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	return v
}

func publishedModule(t *testing.T, svc *service.ModuleService, tenantID, userID uuid.UUID, scope domain.CatalogScope, xml string) (*domain.Module, *domain.ModuleVersion) {
	t.Helper()
	ctx := context.Background()
	mod, draft, err := svc.Create(ctx, tenantID, userID, service.CreateModuleReq{Name: "Library", BPMNXML: xml, Scope: scope})
	if err != nil {
		t.Fatalf("create module: %v", err)
	}
	v, err := svc.Publish(ctx, tenantID, userID, mod.ID, draft.ID)
	if err != nil {
		t.Fatalf("publish module: %v", err)
	}
	return mod, v
}

func TestE2E_ModuleArchive_RefusedWhileInUse(t *testing.T) {
	ctx := context.Background()
	ownerPool, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantID, userID := uuid.New(), uuid.New()
	tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)
	svc := wireModuleService(tenantPool, systemPool)
	versions := pgadapter.NewWorkflowVersionRepo(ownerPool)

	mod, modVersion := publishedModule(t, svc, tenantID, userID, domain.ScopeTenant, minimalBPMN)

	draft := seedCallerWorkflow(t, ownerPool, tenantID, "caller-draft", callsP1BPMN)
	err := svc.Archive(ctx, tenantID, userID, mod.ID)
	var inUse *domain.ModuleInUseError
	if !errors.As(err, &inUse) || len(inUse.Uses) != 1 ||
		inUse.Uses[0].WorkflowID != draft.WorkflowID || inUse.Uses[0].Status != domain.VersionStatusDraft || inUse.Uses[0].VersionNumber != nil {
		t.Fatalf("Archive with a calling draft = %v (%+v), want ErrModuleInUse listing the draft", err, inUse)
	}

	if err := versions.DeleteDraft(ctx, tenantID, draft.ID); err != nil {
		t.Fatalf("delete draft: %v", err)
	}
	published := seedCallerWorkflow(t, ownerPool, tenantID, "caller-published", "<definitions/>")
	ref := domain.ModuleRef{ModuleID: mod.ID, VersionID: modVersion.ID, ProcessID: mod.ProcessID, VersionNumber: *modVersion.VersionNumber}
	if err := versions.Publish(ctx, tenantID, published.ID, 1, 1, `{}`, "h", []domain.ModuleRef{ref}); err != nil {
		t.Fatalf("publish caller: %v", err)
	}
	err = svc.Archive(ctx, tenantID, userID, mod.ID)
	if !errors.As(err, &inUse) || len(inUse.Uses) != 1 ||
		inUse.Uses[0].WorkflowID != published.WorkflowID || inUse.Uses[0].Status != domain.VersionStatusPublished ||
		inUse.Uses[0].VersionNumber == nil || *inUse.Uses[0].VersionNumber != 1 {
		t.Fatalf("Archive with a published version = %v (%+v), want ErrModuleInUse listing version 1", err, inUse)
	}

	if err := versions.Archive(ctx, tenantID, published.ID); err != nil {
		t.Fatalf("archive caller version: %v", err)
	}
	if err := svc.Archive(ctx, tenantID, userID, mod.ID); err != nil {
		t.Fatalf("Archive once only an archived version uses it = %v, want nil", err)
	}
}

func TestE2E_ModuleArchive_GlobalModuleSeesOnlyTheCallersTenant(t *testing.T) {
	ctx := context.Background()
	ownerPool, dsn := fixtures.NewTestPoolAndDSN(t)
	caller, other, userID := uuid.New(), uuid.New(), uuid.New()
	tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &caller, &userID)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)
	svc := wireModuleService(tenantPool, systemPool)
	mod, _ := publishedModule(t, svc, caller, userID, domain.ScopeGlobal, globalModuleBPMN)

	seedCallerWorkflow(t, ownerPool, other, "other-tenant-caller", callsP1BPMN)
	callersDraft := seedCallerWorkflow(t, ownerPool, caller, "callers-draft", callsP1BPMN)

	err := svc.Archive(ctx, caller, userID, mod.ID)
	var inUse *domain.ModuleInUseError
	if !errors.As(err, &inUse) || len(inUse.Uses) != 1 || inUse.Uses[0].WorkflowID != callersDraft.WorkflowID {
		t.Fatalf("Archive of a global module = %v (%+v), want ErrModuleInUse listing only the caller's draft", err, inUse)
	}

	if err := pgadapter.NewWorkflowVersionRepo(ownerPool).DeleteDraft(ctx, caller, callersDraft.ID); err != nil {
		t.Fatalf("delete draft: %v", err)
	}
	if err := svc.Archive(ctx, caller, userID, mod.ID); err != nil {
		t.Fatalf("Archive of a global module used only by another tenant's draft = %v, want nil", err)
	}
}
