package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestArchiveModule_InUse_Returns409WithTheUses(t *testing.T) {
	publishedWF, draftWF := uuid.New(), uuid.New()
	four := int32(4)
	inUse := &domain.ModuleInUseError{Uses: []domain.ModuleUse{
		{WorkflowID: publishedWF, WorkflowKey: "procurement", Status: domain.VersionStatusPublished, VersionNumber: &four},
		{WorkflowID: draftWF, WorkflowKey: "onboarding", Status: domain.VersionStatusDraft},
	}}
	cases := []struct {
		name  string
		scope domain.CatalogScope
		req   func(method, path string, body any) *http.Request
	}{
		{name: "tenant module, tenant admin", scope: domain.ScopeTenant, req: adminReq},
		{name: "global module, platform operator", scope: domain.ScopeGlobal, req: platformOperatorReq},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var archives int
			h := newModuleHandler(&fakeModuleSvc{
				get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
					return &domain.Module{ID: testWFID, Scope: tc.scope}, nil, nil
				},
				archive: func(_ context.Context, tenantID, userID, moduleID uuid.UUID) error {
					archives++
					assert.Equal(t, testTenantID, tenantID)
					assert.Equal(t, testWFID, moduleID)
					return fmt.Errorf("archive module: %w", inUse)
				},
			})

			w := do(newRouter(h), tc.req(http.MethodDelete, "/api/v1/modules/"+testWFID.String(), nil))

			require.Equal(t, http.StatusConflict, w.Code)
			assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
			assert.Equal(t, 1, archives)
			prob := decodeProblem(t, w)
			assert.Equal(t, "MODULE_IN_USE", prob["code"])
			assert.Equal(t, "Module In Use", prob["title"])
			assert.Equal(t, float64(http.StatusConflict), prob["status"])
			assert.Equal(t, "https://api.workflow.platform/errors/conflict", prob["type"])
			assert.Equal(t, "/api/v1/modules/"+testWFID.String(), prob["instance"])
			assert.Equal(t, "the module is used by published workflow versions or workflow drafts; remove those uses before archiving it", prob["detail"])
			assert.Equal(t, []any{
				map[string]any{
					"workflow_id": publishedWF.String(), "workflow_key": "procurement",
					"status": "PUBLISHED", "version_number": float64(4),
				},
				map[string]any{
					"workflow_id": draftWF.String(), "workflow_key": "onboarding", "status": "DRAFT",
				},
			}, prob["uses"])
			assert.NotContains(t, w.Body.String(), "archive module")
			assert.NotContains(t, w.Body.String(), "module is in use")
		})
	}
}

func TestArchiveModule_OtherConflicts_CarryNoUses(t *testing.T) {
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
		},
		archive: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
			return fmt.Errorf("archive module: %w", domain.ErrNoActiveVersion)
		},
	})

	w := do(newRouter(h), adminReq(http.MethodDelete, "/api/v1/modules/"+testWFID.String(), nil))

	require.Equal(t, http.StatusConflict, w.Code)
	prob := decodeProblem(t, w)
	assert.Equal(t, "NO_ACTIVE_VERSION", prob["code"])
	assert.NotContains(t, prob, "uses")
}
