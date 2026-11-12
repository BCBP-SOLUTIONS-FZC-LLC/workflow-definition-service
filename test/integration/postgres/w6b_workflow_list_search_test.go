//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type w6bSeedWorkflow struct {
	key, name, description string
}

func w6bSeedWorkflows(t *testing.T, repo *postgres.WorkflowRepo, tenantID uuid.UUID, seeds []w6bSeedWorkflow) {
	t.Helper()
	for _, s := range seeds {
		wf := &domain.Workflow{
			ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(),
			TemplateKey: s.key, Name: s.name, Description: s.description,
		}
		require.NoError(t, repo.Create(context.Background(), wf), s.key)
	}
}

func TestWorkflowRepo_List_SearchMatchesNameOnlyIgnoringCase(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()
	seeds := []w6bSeedWorkflow{
		{key: "onboarding", name: "Onboarding", description: "new hires"},
		{key: "dash-setup", name: "Dashboard Setup", description: "wiring"},
		{key: "board-flow", name: "Alpha", description: "board notes about the flow"},
		{key: "tender-review", name: "Tender Review", description: "standard"},
	}
	cases := []struct {
		name   string
		search string
		want   []string
	}{
		{"upper case term matches inside lower and mixed case names", "BOARD", []string{"Dashboard Setup", "Onboarding"}},
		{"lower case term matches the same names", "board", []string{"Dashboard Setup", "Onboarding"}},
		{"mixed case term matches the same names", "bOaRd", []string{"Dashboard Setup", "Onboarding"}},
		{"a term found only in the template key matches nothing", "dash-setup", nil},
		{"a term found only in the description matches nothing", "notes about", nil},
		{"a term in one name only", "TENDER", []string{"Tender Review"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID := uuid.New()
			w6bSeedWorkflows(t, repo, tenantID, seeds)
			w6bSeedWorkflows(t, repo, uuid.New(), []w6bSeedWorkflow{{key: "other-board", name: "Other Tenant Board", description: ""}})
			search := tc.search

			results, total, err := repo.List(ctx, tenantID, port.WorkflowFilter{Search: &search, Page: 1, Limit: 50})

			require.NoError(t, err)
			var got []string
			for _, wf := range results {
				got = append(got, wf.Name)
				assert.Equal(t, tenantID, wf.TenantID)
			}
			assert.ElementsMatch(t, tc.want, got)
			assert.Equal(t, int64(len(tc.want)), total)
		})
	}
}
