package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres/db"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestStarterFromDB_NullOptionalColumns_AreAnEmptyStringOrNilAndTheRestIsCarriedOver(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	created := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)

	got := starterFromDB(db.GetStarterByIDRow{
		ID:            id,
		Scope:         db.CatalogScopeGlobal,
		Name:          "Onboarding",
		BpmnXml:       "<definitions/>",
		CreatedAt:     created,
		UpdatedAt:     updated,
		RecordVersion: 4,
	})

	require.NotNil(t, got)
	assert.Equal(t, id, got.ID)
	assert.Equal(t, domain.CatalogScope(db.CatalogScopeGlobal), got.Scope)
	assert.Equal(t, "Onboarding", got.Name)
	assert.Equal(t, "<definitions/>", got.BPMNXML)
	assert.Equal(t, int64(4), got.RecordVersion)
	assert.Equal(t, created, got.CreatedAt)
	assert.Equal(t, updated, got.UpdatedAt)
	assert.Empty(t, got.Description, "a NULL description is an empty string")
	assert.Empty(t, got.Category, "a NULL category is an empty string")
	assert.Nil(t, got.TenantID)
	assert.Nil(t, got.SourceWorkflowVersionID)
	assert.Nil(t, got.CreatedByUserID)
}

func TestStarterFromDB_SetOptionalColumns_AreCarriedOver(t *testing.T) {
	t.Parallel()

	tenant, source, author := uuid.New(), uuid.New(), uuid.New()

	got := starterFromDB(db.GetStarterByIDRow{
		ID:                      uuid.New(),
		TenantID:                pgtype.UUID{Bytes: tenant, Valid: true},
		Scope:                   db.CatalogScopeTenant,
		Description:             pgtype.Text{String: "For new hires", Valid: true},
		Category:                pgtype.Text{String: "hr", Valid: true},
		SourceWorkflowVersionID: pgtype.UUID{Bytes: source, Valid: true},
		CreatedByUserID:         pgtype.UUID{Bytes: author, Valid: true},
	})

	assert.Equal(t, domain.CatalogScope(db.CatalogScopeTenant), got.Scope)
	assert.Equal(t, "For new hires", got.Description)
	assert.Equal(t, "hr", got.Category)
	require.NotNil(t, got.TenantID)
	assert.Equal(t, tenant, *got.TenantID)
	require.NotNil(t, got.SourceWorkflowVersionID)
	assert.Equal(t, source, *got.SourceWorkflowVersionID)
	require.NotNil(t, got.CreatedByUserID)
	assert.Equal(t, author, *got.CreatedByUserID)
}
