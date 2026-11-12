package service

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
)

func planWithDepartments(ids ...string) *dsl.CompiledPlan {
	plan := &dsl.CompiledPlan{}
	for _, id := range ids {
		plan.Departments = append(plan.Departments, dsl.DepartmentDef{ID: id})
	}
	return plan
}

func TestComputeDiff_DepartmentLists_AreInSortedOrderEveryTime(t *testing.T) {
	var baseIDs, targetIDs, wantAdded, wantRemoved []string
	for i := 1; i <= 8; i++ {
		kept := fmt.Sprintf("kept-%d", i)
		baseIDs = append(baseIDs, kept, fmt.Sprintf("old-%d", 9-i))
		targetIDs = append(targetIDs, kept, fmt.Sprintf("new-%d", 9-i))
		wantAdded = append(wantAdded, fmt.Sprintf("new-%d", i))
		wantRemoved = append(wantRemoved, fmt.Sprintf("old-%d", i))
	}
	base, target := planWithDepartments(baseIDs...), planWithDepartments(targetIDs...)

	for run := 0; run < 100; run++ {
		changes := computeDiff(base, target)

		require.Equal(t, wantAdded, changes.AddedDepartments, "run %d", run)
		require.Equal(t, wantRemoved, changes.RemovedDepartments, "run %d", run)
		require.False(t, changes.MetadataOnly)
	}
}
