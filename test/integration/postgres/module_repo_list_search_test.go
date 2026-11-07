package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func TestModuleRepo_List_SearchTerm_IsBoundAsLiteralText(t *testing.T) {
	cases := []struct {
		name      string
		search    string
		wantNames []string
	}{
		{
			name:      "quote injection matches only the module whose name contains it",
			search:    "' OR 1=1 --",
			wantNames: []string{"x' OR 1=1 -- y"},
		},
		{
			name:      "statement terminator is literal text",
			search:    "'; DROP TABLE workflow_module; --",
			wantNames: nil,
		},
		{
			name:      "term of 250 characters matches the module whose name contains it",
			search:    strings.Repeat("long-", 50),
			wantNames: []string{strings.Repeat("long-", 50) + "tail"},
		},
		{
			name:      "term longer than any name is accepted and matches nothing",
			search:    strings.Repeat("long-", 60),
			wantNames: nil,
		},
		{
			name:      "unicode term matches the module with that text",
			search:    "承認フロー Über",
			wantNames: []string{"承認フロー Über Kette"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)
			repo := postgres.NewModuleRepo(pool)
			ctx := context.Background()
			tenantID := uuid.New()

			for _, name := range []string{
				"x' OR 1=1 -- y", strings.Repeat("long-", 50) + "tail", "承認フロー Über Kette",
				"Plain Alpha", "Plain Beta",
			} {
				m := &domain.Module{
					ID: uuid.New(), ProcessID: "Process_" + uuid.NewString(),
					TenantID: &tenantID, Scope: domain.ScopeTenant, Name: name,
				}
				if err := repo.Create(ctx, m); err != nil {
					t.Fatalf("create %q: %v", name, err)
				}
			}

			search := tc.search
			results, total, err := repo.List(ctx, tenantID, port.ModuleFilter{Search: &search})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			var got []string
			for _, m := range results {
				got = append(got, m.Name)
			}
			if total != int64(len(tc.wantNames)) || len(got) != len(tc.wantNames) {
				t.Fatalf("total=%d names=%q, want exactly %q", total, got, tc.wantNames)
			}
			for i := range got {
				if got[i] != tc.wantNames[i] {
					t.Errorf("name[%d] = %q, want %q", i, got[i], tc.wantNames[i])
				}
			}

			all, allTotal, err := repo.List(ctx, tenantID, port.ModuleFilter{})
			if err != nil {
				t.Fatalf("List after search: %v", err)
			}
			if allTotal != 5 || len(all) != 5 {
				t.Errorf("after the search the tenant has total=%d len=%d modules, want 5", allTotal, len(all))
			}
		})
	}
}
