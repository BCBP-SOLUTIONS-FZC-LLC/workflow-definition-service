package element

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// TestPatchEmptyDepts_ExclusiveRevertPath verifies that patchEmptyDepts fills
// in RevertToDept on an exclusive branch that is a guarded-loop revert (has
// RevertToStage set) but has no RevertToDept of its own — it must inherit the
// parent lane, the same way a forward Target does.
func TestPatchEmptyDepts_ExclusiveRevertPath(t *testing.T) {
	steps := []domain.ExecutionStep{
		{
			Exclusive: []domain.ExclusiveBranch{
				{RevertToStage: "prep", RevertToDept: ""},
			},
		},
	}

	patchEmptyDepts(steps, "engineering")

	got := steps[0].Exclusive[0].RevertToDept
	if got != "engineering" {
		t.Errorf("RevertToDept = %q, want %q", got, "engineering")
	}
}
