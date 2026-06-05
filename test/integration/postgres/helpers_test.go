package postgres_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func newTestPool(t *testing.T) *pgcommon.Pool {
	t.Helper()
	return fixtures.NewTestPool(t)
}

// newDraftVersion creates a minimal DRAFT WorkflowVersion for test setup.
func newDraftVersion(workflowID, tenantID uuid.UUID) *domain.WorkflowVersion {
	return &domain.WorkflowVersion{
		ID:              uuid.New(),
		WorkflowID:      workflowID,
		TenantID:        tenantID,
		Status:          domain.VersionStatusDraft,
		BPMNXML:         "<bpmn/>",
		CreatedByUserID: uuid.New(),
		IsValid:         true,
	}
}
