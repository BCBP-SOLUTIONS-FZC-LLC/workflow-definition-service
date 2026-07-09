package service

import (
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// TestExtractAssignees_InvalidUUID verifies that extractAssignees skips a
// DefaultAssignees entry that isn't a parseable UUID rather than erroring out
// or emitting a zero-value assignee row.
func TestExtractAssignees_InvalidUUID(t *testing.T) {
	tenantID := uuid.New()
	versionID := uuid.New()
	plan := &domain.CompiledPlan{
		Departments: []domain.DepartmentDef{
			{
				ID: "engineering",
				Stages: []domain.StageDef{
					{
						Type:             "prep",
						Role:             "preparer",
						DefaultAssignees: []string{"not-a-uuid", uuid.New().String()},
					},
				},
			},
		},
	}

	got := extractAssignees(tenantID, versionID, plan)
	if len(got) != 1 {
		t.Fatalf("extractAssignees() returned %d assignees, want 1 (invalid UUID skipped); got %+v", len(got), got)
	}
}
