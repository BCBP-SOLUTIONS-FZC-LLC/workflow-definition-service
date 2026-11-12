//go:build integration

package postgres_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var wildcardSearchCases = []struct {
	name   string
	search string
	want   []string
}{
	{name: "percent matches only names holding a percent sign", search: "%", want: []string{"100% done", "a%b"}},
	{name: "underscore matches only names holding an underscore", search: "_", want: []string{"snake_case", "a_b"}},
	{name: "percent between letters is literal", search: "a%b", want: []string{"a%b"}},
	{name: "underscore between letters is literal", search: "a_b", want: []string{"a_b"}},
	{name: "backslash is literal", search: `\`, want: []string{`back\slash`}},
	{name: "escaped percent form is literal text", search: `\%`, want: nil},
	{name: "plain term still matches case-insensitively", search: "PLAIN", want: []string{"plain name"}},
}

var wildcardSeedNames = []string{"100% done", "snake_case", "a%b", "a_b", "axb", `back\slash`, "plain name"}

func sortedCopy(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}

func TestModuleRepo_List_SearchWildcards_AreLiteralText(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewModuleRepo(pool)
	ctx := context.Background()
	for _, tc := range wildcardSearchCases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID := uuid.New()
			for _, name := range wildcardSeedNames {
				m := &domain.Module{
					ID: uuid.New(), ProcessID: "Process_" + uuid.NewString(),
					TenantID: &tenantID, Scope: domain.ScopeTenant, Name: name,
				}
				require.NoError(t, repo.Create(ctx, m))
			}
			search := tc.search

			results, total, err := repo.List(ctx, tenantID, port.ModuleFilter{Search: &search})

			require.NoError(t, err)
			var got []string
			for _, m := range results {
				got = append(got, m.Name)
			}
			require.Equal(t, sortedCopy(tc.want), sortedCopy(got))
			require.Equal(t, int64(len(tc.want)), total)
		})
	}
}

func TestStarterRepo_List_SearchWildcards_AreLiteralText(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewStarterRepo(pool)
	ctx := context.Background()
	for _, tc := range wildcardSearchCases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID := uuid.New()
			for _, name := range wildcardSeedNames {
				s := &domain.StarterTemplate{
					ID: uuid.New(), TenantID: &tenantID, Scope: domain.ScopeTenant,
					Name: name, Category: "general", BPMNXML: "<bpmn/>",
				}
				require.NoError(t, repo.Create(ctx, s))
			}
			search := tc.search

			results, total, err := repo.List(ctx, tenantID, port.StarterFilter{Search: &search})

			require.NoError(t, err)
			var got []string
			for _, s := range results {
				got = append(got, s.Name)
			}
			require.Equal(t, sortedCopy(tc.want), sortedCopy(got))
			require.Equal(t, int64(len(tc.want)), total)
		})
	}
}

func TestWorkflowRepo_List_SearchWildcards_AreLiteralText(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()
	for _, tc := range wildcardSearchCases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID := uuid.New()
			for i, name := range wildcardSeedNames {
				wf := &domain.Workflow{
					ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(),
					TemplateKey: "wildcard-" + string(rune('a'+i)), Name: name,
				}
				require.NoError(t, repo.Create(ctx, wf))
			}
			search := tc.search

			results, total, err := repo.List(ctx, tenantID, port.WorkflowFilter{Search: &search})

			require.NoError(t, err)
			var got []string
			for _, w := range results {
				got = append(got, w.Name)
			}
			require.Equal(t, sortedCopy(tc.want), sortedCopy(got))
			require.Equal(t, int64(len(tc.want)), total)
		})
	}
}
