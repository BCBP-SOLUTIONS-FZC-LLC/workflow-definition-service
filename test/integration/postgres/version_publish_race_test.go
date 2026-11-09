package postgres_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func publishRaceSeedDraft(t *testing.T, pool *pgcommon.Pool, tenantID uuid.UUID, key, bpmnXML string) (*domain.Workflow, *domain.WorkflowVersion) {
	t.Helper()
	ctx := context.Background()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: key, Name: "X",
	}
	if err := postgres.NewWorkflowRepo(pool).Create(ctx, wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	draft := newDraftVersion(wf.ID, tenantID)
	draft.BPMNXML = bpmnXML
	if err := postgres.NewWorkflowVersionRepo(pool).Create(ctx, draft); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	return wf, draft
}

func TestWorkflowVersionRepo_Publish_VersionPublishedAfterRead_ReturnsVersionNotDraft(t *testing.T) {
	pool := newTestPool(t)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	tenantID := uuid.New()
	_, draft := publishRaceSeedDraft(t, pool, tenantID, "pub-race-stale", "<definitions/>")

	readAt, err := vRepo.GetByID(ctx, tenantID, draft.ID)
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	if err := vRepo.Publish(ctx, tenantID, draft.ID, readAt.RecordVersion, 1, `{"steps":[]}`, "first-hash", nil); err != nil {
		t.Fatalf("first publish: %v", err)
	}

	err = vRepo.Publish(ctx, tenantID, draft.ID, readAt.RecordVersion, 2, `{"steps":["second"]}`, "second-hash", nil)
	if !errors.Is(err, domain.ErrVersionNotDraft) {
		t.Fatalf("Publish of a version that is no longer a draft = %v, want ErrVersionNotDraft", err)
	}

	got, err := vRepo.GetByID(ctx, tenantID, draft.ID)
	if err != nil {
		t.Fatalf("read after refused publish: %v", err)
	}
	if got.Status != domain.VersionStatusPublished || got.VersionNumber == nil || *got.VersionNumber != 1 || got.ArtifactHash != "first-hash" {
		t.Errorf("refused publish changed the row: status %v, number %v, hash %q; want the first publish untouched",
			got.Status, got.VersionNumber, got.ArtifactHash)
	}
}

func TestWorkflowVersionRepo_Publish_AnotherTenantsVersion_IsNotFoundUnderRLS(t *testing.T) {
	ownerPool, dsn := fixtures.NewTestPoolAndDSN(t)
	ownerTenant, callerTenant, callerUser := uuid.New(), uuid.New(), uuid.New()
	_, draft := publishRaceSeedDraft(t, ownerPool, ownerTenant, "pub-rls-owner", "<definitions/>")
	callerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &callerTenant, &callerUser)
	ctx := context.Background()

	if _, err := postgres.NewWorkflowVersionRepo(ownerPool).GetByID(ctx, ownerTenant, draft.ID); err != nil {
		t.Fatalf("control: the owner cannot read its own draft: %v", err)
	}

	callerRepo := postgres.NewWorkflowVersionRepo(callerPool)
	if _, err := callerRepo.GetByID(ctx, ownerTenant, draft.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID of another tenant's draft with its tenant id = %v, want ErrNotFound", err)
	}
	if err := callerRepo.Publish(ctx, ownerTenant, draft.ID, 1, 1, `{"steps":[]}`, "h", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Publish of another tenant's draft with its tenant id = %v, want ErrNotFound", err)
	}

	got, err := postgres.NewWorkflowVersionRepo(ownerPool).GetByID(ctx, ownerTenant, draft.ID)
	if err != nil {
		t.Fatalf("owner read after the attempts: %v", err)
	}
	if got.Status != domain.VersionStatusDraft {
		t.Errorf("another tenant's publish changed the draft to %v", got.Status)
	}
}

func TestVersionService_Publish_TwoRequestsForOneDraft_PublishesOnce(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	bpmnXML, err := os.ReadFile("../../unit/service/testdata/single-process.bpmn")
	if err != nil {
		t.Fatalf("read bpmn fixture: %v", err)
	}
	tenantID, userID := uuid.New(), uuid.New()
	wf, draft := publishRaceSeedDraft(t, pool, tenantID, "pub-race-two", string(bpmnXML))

	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor:     postgres.NewTransactor(pool),
		Workflows:      wfRepo,
		Versions:       vRepo,
		Assignees:      postgres.NewAssigneeRepo(pool),
		Compiler:       bpmn_compiler.New(),
		ModuleVersions: postgres.NewModuleVersionRepo(pool),
	})

	const callers = 8
	errs := make([]error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = svc.Publish(ctx, tenantID, userID, wf.ID, draft.ID, false)
		}()
	}
	close(start)
	wg.Wait()

	var succeeded, refused int
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, domain.ErrVersionNotDraft), errors.Is(err, domain.ErrDraftConcurrency):
			refused++
		default:
			t.Errorf("a losing publish failed with %v, want INVALID_VERSION_STATUS or DRAFT_CONCURRENCY", err)
		}
	}
	if succeeded != 1 || refused != callers-1 {
		t.Fatalf("outcomes = %v, want exactly one success and %d refusals", errs, callers-1)
	}

	got, err := vRepo.GetByID(ctx, tenantID, draft.ID)
	if err != nil {
		t.Fatalf("read published version: %v", err)
	}
	if got.Status != domain.VersionStatusPublished || got.VersionNumber == nil || *got.VersionNumber != 1 {
		t.Errorf("version = status %v number %v, want PUBLISHED number 1", got.Status, got.VersionNumber)
	}
	reloaded, err := wfRepo.GetByID(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	if reloaded.ActiveVersionID == nil || *reloaded.ActiveVersionID != draft.ID {
		t.Errorf("active version = %v, want %v", reloaded.ActiveVersionID, draft.ID)
	}
}

func TestModuleVersionRepo_Resolve_AClosedPoolIsUnavailableNotAnInternalError(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleVersionRepo(pool)
	pool.Close()

	_, err := repo.Resolve(context.Background(), uuid.New(), "Process_Review", 0)
	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Errorf("Resolve on a closed pool = %v, want ErrUpstreamUnavailable so publish answers 503", err)
	}
}
