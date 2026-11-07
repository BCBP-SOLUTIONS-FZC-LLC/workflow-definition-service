package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func TestModuleRepo_List_FailureAfterTheCount_ReturnsAPlainErrorAndNoRows(t *testing.T) {
	t.Parallel()

	queryFailed := errors.New("connection reset by peer")
	scanFailed := errors.New("cannot scan into *time.Time")
	iterFailed := errors.New("unexpected EOF")

	cases := []struct {
		name    string
		tx      *workflowListTx
		wantErr error
		wantMsg string
	}{
		{
			name:    "data query fails",
			tx:      &workflowListTx{queryErr: queryFailed},
			wantErr: queryFailed,
			wantMsg: "list modules: ",
		},
		{
			name:    "row scan fails",
			tx:      &workflowListTx{rows: &listRows{remaining: 1, scanErr: scanFailed}},
			wantErr: scanFailed,
			wantMsg: "list modules scan: ",
		},
		{
			name:    "row iteration fails",
			tx:      &workflowListTx{rows: &listRows{remaining: 0, iterErr: iterFailed}},
			wantErr: iterFailed,
			wantMsg: "list modules rows: ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := &ModuleRepo{}

			results, total, err := repo.List(
				withTx(context.Background(), tc.tx), uuid.New(), port.ModuleFilter{Page: 1, Limit: 20})

			if err == nil {
				t.Fatal("expected an error")
			}
			if results != nil || total != 0 {
				t.Errorf("results=%v total=%d, want none on failure", results, total)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want it to wrap %v", err, tc.wantErr)
			}
			if errors.Is(err, domain.ErrUpstreamUnavailable) || errors.Is(err, domain.ErrNotFound) {
				t.Errorf("err = %v, want a plain error the API reports as an internal error", err)
			}
			if got := err.Error(); !strings.HasPrefix(got, tc.wantMsg) {
				t.Errorf("err = %q, want prefix %q", got, tc.wantMsg)
			}
		})
	}
}

func TestModuleRepo_List_OutOfRangePaging_UsesDefaults(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		page       int
		limit      int
		wantLimit  int32
		wantOffset int32
	}{
		{"page and limit zero", 0, 0, 20, 0},
		{"page and limit negative", -2, -9, 20, 0},
		{"page zero keeps explicit limit", 0, 5, 5, 0},
		{"limit zero keeps explicit page", 3, 0, 20, 40},
		{"valid page and limit", 2, 10, 10, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tx := &workflowListTx{rows: &listRows{}}
			repo := &ModuleRepo{}
			tenantID := uuid.New()

			_, total, err := repo.List(
				withTx(context.Background(), tx), tenantID, port.ModuleFilter{Page: tc.page, Limit: tc.limit})

			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if total != 7 {
				t.Errorf("total = %d, want the counted 7", total)
			}
			if len(tx.queryArgs) != 3 {
				t.Fatalf("data query args = %v, want tenant, limit, offset", tx.queryArgs)
			}
			if got := tx.queryArgs[0]; got != tenantID {
				t.Errorf("tenant arg = %v, want %v", got, tenantID)
			}
			if got := tx.queryArgs[1]; got != tc.wantLimit {
				t.Errorf("limit = %v, want %d", got, tc.wantLimit)
			}
			if got := tx.queryArgs[2]; got != tc.wantOffset {
				t.Errorf("offset = %v, want %d", got, tc.wantOffset)
			}
		})
	}
}
