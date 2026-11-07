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
)

type sgDModuleVersionsCall struct {
	tenantID, moduleID uuid.UUID
	page, limit        int
}

func TestListModuleVersions_NoQuery_UsesPageOneLimitTwentyAndBuildsEachItem(t *testing.T) {
	t.Parallel()

	number := int32(2)
	publishedAt := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	createdAt := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	draftID, publishedID := uuid.New(), uuid.New()
	var call *sgDModuleVersionsCall
	h := newModuleHandler(&fakeModuleSvc{
		listVersions: func(_ context.Context, tenantID, moduleID uuid.UUID, page, limit int) ([]*domain.ModuleVersion, int64, error) {
			call = &sgDModuleVersionsCall{tenantID: tenantID, moduleID: moduleID, page: page, limit: limit}
			return []*domain.ModuleVersion{
				{ID: draftID, ModuleID: testWFID, Scope: domain.ScopeTenant, Status: domain.VersionStatusDraft, BPMNXML: "<draft/>", ProcessID: "Process_A", IsValid: false, CreatedAt: createdAt},
				{ID: publishedID, ModuleID: testWFID, Scope: domain.ScopeGlobal, Status: domain.VersionStatusPublished, BPMNXML: "<pub/>", ProcessID: "Process_A", IsValid: true, VersionNumber: &number, PublishedAt: &publishedAt, CreatedAt: createdAt},
			}, 2, nil
		},
	})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules/"+testWFID.String()+"/versions", nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, call)
	assert.Equal(t, sgDModuleVersionsCall{tenantID: testTenantID, moduleID: testWFID, page: 1, limit: 20}, *call)
	var resp struct {
		Versions   []map[string]any `json:"versions"`
		Pagination map[string]any   `json:"pagination"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, map[string]any{"total_count": float64(2), "page": float64(1), "limit": float64(20)}, resp.Pagination)
	require.Len(t, resp.Versions, 2)

	assert.Equal(t, map[string]any{
		"id": draftID.String(), "module_id": testWFID.String(), "scope": "tenant", "status": "DRAFT",
		"bpmn_xml": "<draft/>", "process_id": "Process_A", "is_valid": false, "created_at": "2026-04-01T00:00:00Z",
	}, resp.Versions[0])
	assert.Equal(t, map[string]any{
		"id": publishedID.String(), "module_id": testWFID.String(), "scope": "global", "status": "PUBLISHED",
		"bpmn_xml": "<pub/>", "process_id": "Process_A", "is_valid": true, "version_number": float64(2),
		"published_at": "2026-04-05T06:07:08Z", "created_at": "2026-04-01T00:00:00Z",
	}, resp.Versions[1])
}

func TestListModuleVersions_ExplicitPaging_IsPassedOnAndEchoed(t *testing.T) {
	t.Parallel()

	var call *sgDModuleVersionsCall
	h := newModuleHandler(&fakeModuleSvc{
		listVersions: func(_ context.Context, tenantID, moduleID uuid.UUID, page, limit int) ([]*domain.ModuleVersion, int64, error) {
			call = &sgDModuleVersionsCall{tenantID: tenantID, moduleID: moduleID, page: page, limit: limit}
			return nil, 7, nil
		},
	})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules/"+testWFID.String()+"/versions?page=2&limit=5", nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, call)
	assert.Equal(t, sgDModuleVersionsCall{tenantID: testTenantID, moduleID: testWFID, page: 2, limit: 5}, *call)
	assert.JSONEq(t, `{"versions": [], "pagination": {"total_count": 7, "page": 2, "limit": 5}}`, w.Body.String())
}
