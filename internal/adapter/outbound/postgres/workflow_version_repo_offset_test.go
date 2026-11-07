package postgres

import (
	"context"
	"math"
	"testing"

	"github.com/google/uuid"
)

func TestWorkflowVersionRepo_ListByWorkflow_OffsetBeyondInt32_IsClampedToTheLargestOffset(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		page       int
		limit      int
		wantLimit  int32
		wantOffset int32
	}{
		{"offset just above int32", math.MaxInt32/20 + 2, 20, 20, math.MaxInt32},
		{"offset far above int32", 100_000_000, 100, 100, math.MaxInt32},
		{"first page with the largest limit has offset zero", 1, math.MaxInt32, math.MaxInt32, 0},
		{"offset exactly at int32 max", math.MaxInt32 + 1, 1, 1, math.MaxInt32},
		{"page and limit both above int32", math.MaxInt32, math.MaxInt32, math.MaxInt32, math.MaxInt32},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tx := &versionListTx{count: countRow{total: 0}}

			_, _, err := (&WorkflowVersionRepo{}).ListByWorkflow(
				withTx(context.Background(), tx), uuid.New(), uuid.New(), tc.page, tc.limit)

			if err != nil {
				t.Fatalf("ListByWorkflow: %v", err)
			}
			if len(tx.listArgs) != 4 {
				t.Fatalf("list query args = %v, want 4", tx.listArgs)
			}
			if got := tx.listArgs[2]; got != tc.wantLimit {
				t.Errorf("limit = %v, want %d", got, tc.wantLimit)
			}
			if got := tx.listArgs[3]; got != tc.wantOffset {
				t.Errorf("offset = %v, want %d", got, tc.wantOffset)
			}
		})
	}
}
