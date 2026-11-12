//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestStarterRepo_Create_ReturnsTheStoredTimestamps(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewStarterRepo(pool)
	ctx := context.Background()

	tests := []struct {
		name string
		make func(tenantID uuid.UUID) *domain.StarterTemplate
	}{
		{
			name: "tenant starter",
			make: func(tenantID uuid.UUID) *domain.StarterTemplate {
				return &domain.StarterTemplate{
					ID: uuid.New(), TenantID: &tenantID, Scope: domain.ScopeTenant,
					Name: "Onboarding", BPMNXML: "<bpmn/>",
				}
			},
		},
		{
			name: "global starter",
			make: func(uuid.UUID) *domain.StarterTemplate {
				return &domain.StarterTemplate{
					ID: uuid.New(), Scope: domain.ScopeGlobal, Name: "Global", BPMNXML: "<bpmn/>",
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tenantID := uuid.New()
			st := tc.make(tenantID)

			if err := repo.Create(ctx, st); err != nil {
				t.Fatalf("Create: %v", err)
			}
			if st.CreatedAt.IsZero() || st.UpdatedAt.IsZero() {
				t.Fatalf("Create left zero timestamps: created_at=%v updated_at=%v", st.CreatedAt, st.UpdatedAt)
			}

			stored, err := repo.GetByID(ctx, tenantID, st.ID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if !st.CreatedAt.Equal(stored.CreatedAt) {
				t.Errorf("created_at = %v, stored %v", st.CreatedAt, stored.CreatedAt)
			}
			if !st.UpdatedAt.Equal(stored.UpdatedAt) {
				t.Errorf("updated_at = %v, stored %v", st.UpdatedAt, stored.UpdatedAt)
			}
		})
	}
}
