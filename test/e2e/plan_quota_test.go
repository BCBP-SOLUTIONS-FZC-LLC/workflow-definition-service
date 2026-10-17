//go:build integration

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func TestE2E_TemplateLimit_RefusesAtTheLimitAndArchivingFreesASlot(t *testing.T) {
	ctx := context.Background()
	svcs := wireServices(t, fixtures.NewTestPool(t), nil)
	tenantID, userID := uuid.New(), uuid.New()

	var first *domain.Workflow
	var firstDraft *domain.WorkflowVersion
	for i := range 5 {
		wf, draft, err := svcs.workflow.Create(ctx, tenantID, userID, fmt.Sprintf("quota-%d", i), "Quota", "", minimalBPMN, " Starter ")
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		if i == 0 {
			first, firstDraft = wf, draft
		}
	}

	if _, _, err := svcs.workflow.Create(ctx, tenantID, userID, "quota-over", "Over", "", minimalBPMN, "starter"); !errors.Is(err, domain.ErrPlanQuotaExceeded) {
		t.Fatalf("a sixth starter template: err = %v, want ErrPlanQuotaExceeded", err)
	}
	if _, _, err := svcs.workflow.Create(ctx, tenantID, userID, "quota-spoof", "Spoof", "", minimalBPMN, "platinum"); !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Fatalf("an unknown plan code: err = %v, want ErrUpstreamUnavailable", err)
	}
	if _, _, err := svcs.workflow.Create(ctx, uuid.New(), userID, "quota-enterprise", "Enterprise", "", minimalBPMN, "enterprise"); err != nil {
		t.Fatalf("another tenant on an unlimited plan: %v", err)
	}

	published, err := svcs.version.Publish(ctx, tenantID, userID, first.ID, firstDraft.ID, false)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, err := svcs.version.Promote(ctx, tenantID, userID, first.ID, published.ID); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if err := svcs.workflow.Archive(ctx, tenantID, userID, first.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	if _, _, err := svcs.workflow.Create(ctx, tenantID, userID, "quota-after-archive", "After", "", minimalBPMN, "starter"); err != nil {
		t.Fatalf("a template after archiving one: %v", err)
	}
}
