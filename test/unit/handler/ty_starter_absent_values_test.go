package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func tyBareGlobalStarter() *domain.StarterTemplate {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &domain.StarterTemplate{
		ID:        testWFID,
		Scope:     domain.ScopeGlobal,
		Name:      "Bare starter",
		BPMNXML:   "<definitions/>",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func tyAssertBareStarterItem(t *testing.T, item map[string]any) {
	t.Helper()
	assert.Equal(t, testWFID.String(), item["id"])
	assert.Equal(t, "global", item["scope"])
	assert.Equal(t, "Bare starter", item["name"])
	assert.Equal(t, "<definitions/>", item["bpmn_xml"])
	desc, present := item["description"]
	assert.True(t, present, "description is always present")
	assert.Equal(t, "", desc)
	for _, key := range []string{"tenant_id", "category", "source_workflow_version_id"} {
		assert.NotContains(t, item, key, "an absent value is omitted, never null")
	}
}

func TestStarters_GlobalStarterWithoutOptionalValues_OmitsTheKeysAndKeepsAnEmptyDescription(t *testing.T) {
	t.Parallel()

	t.Run("list", func(t *testing.T) {
		t.Parallel()
		var gotTenant uuid.UUID
		h := newStarterHandler(&fakeStarterSvc{
			list: func(_ context.Context, tenantID uuid.UUID, _ port.StarterFilter) ([]*domain.StarterTemplate, int64, error) {
				gotTenant = tenantID
				return []*domain.StarterTemplate{tyBareGlobalStarter()}, 1, nil
			},
		})

		w := do(newRouter(h), req(http.MethodGet, "/api/v1/starters", nil))

		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var body struct {
			Starters []map[string]any `json:"starters"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Len(t, body.Starters, 1)
		tyAssertBareStarterItem(t, body.Starters[0])
		assert.Equal(t, testTenantID, gotTenant)
	})

	t.Run("get", func(t *testing.T) {
		t.Parallel()
		var gotTenant, gotID uuid.UUID
		h := newStarterHandler(&fakeStarterSvc{
			get: func(_ context.Context, tenantID, id uuid.UUID) (*domain.StarterTemplate, error) {
				gotTenant, gotID = tenantID, id
				return tyBareGlobalStarter(), nil
			},
		})

		w := do(newRouter(h), req(http.MethodGet, "/api/v1/starters/"+testWFID.String(), nil))

		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var item map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &item))
		tyAssertBareStarterItem(t, item)
		assert.Equal(t, testTenantID, gotTenant)
		assert.Equal(t, testWFID, gotID)
	})
}
