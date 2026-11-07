package handler_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestGetStarter_TenantStarter_ReturnsEveryListedFieldAndNoInternalOnes(t *testing.T) {
	t.Parallel()

	tenant, source, creator := testTenantID, uuid.New(), uuid.New()
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	stored := &domain.StarterTemplate{
		ID: testWFID, TenantID: &tenant, Scope: domain.ScopeTenant, Name: "Onboarding", Description: "Hires",
		Category: "hr", BPMNXML: "<bpmn id=\"x\"/>", SourceWorkflowVersionID: &source,
		CreatedByUserID: &creator, RecordVersion: 7, CreatedAt: created, UpdatedAt: updated,
	}
	var gotTenant, gotID uuid.UUID
	h := newStarterHandler(&fakeStarterSvc{
		get: func(_ context.Context, callerTenantID, id uuid.UUID) (*domain.StarterTemplate, error) {
			gotTenant, gotID = callerTenantID, id
			return stored, nil
		},
	})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/starters/"+testWFID.String(), nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, testTenantID, gotTenant)
	assert.Equal(t, testWFID, gotID)
	assert.JSONEq(t, `{
	  "id": "`+testWFID.String()+`",
	  "tenant_id": "`+tenant.String()+`",
	  "scope": "tenant",
	  "name": "Onboarding",
	  "description": "Hires",
	  "category": "hr",
	  "bpmn_xml": "<bpmn id=\"x\"/>",
	  "source_workflow_version_id": "`+source.String()+`",
	  "created_at": "2026-01-02T03:04:05Z",
	  "updated_at": "2026-02-03T04:05:06Z"
	}`, w.Body.String())
}

func TestGetStarter_PathIDIsNotAUUID_Returns400BadRequestProblem(t *testing.T) {
	t.Parallel()

	called := false
	h := newStarterHandler(&fakeStarterSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.StarterTemplate, error) {
			called = true
			return nil, nil
		},
	})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/starters/not-a-uuid", nil))

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	prob := decodeProblem(t, w)
	assert.Equal(t, "https://api.workflow.platform/errors/bad-request", prob["type"])
	assert.Equal(t, "BAD_REQUEST", prob["code"])
	assert.Equal(t, "invalid id: not a valid UUID", prob["detail"])
	assert.Equal(t, float64(http.StatusBadRequest), prob["status"])
	assert.Equal(t, "/api/v1/starters/not-a-uuid", prob["instance"])
	assert.NotContains(t, prob, "invalid_params")
	assert.False(t, called, "no query is made for a malformed id")
}
