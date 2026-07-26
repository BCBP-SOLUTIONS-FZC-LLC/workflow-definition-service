package service

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"

	"github.com/google/uuid"
)

// TestExtractAssignees_InvalidUUID verifies that extractAssignees skips a
// DefaultAssignees entry that isn't a parseable UUID rather than erroring out
// or emitting a zero-value assignee row.
func TestExtractAssignees_InvalidUUID(t *testing.T) {
	tenantID := uuid.New()
	versionID := uuid.New()
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{
			{
				ID:              "engineering",
				IAMDepartmentID: uuid.New().String(),
				Stages: []dsl.StageDef{
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
