package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func w8cSortedKeys(body map[string]any) []string {
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestGetModuleVersion_DraftBodyCarriesExactlyTheDraftKeys_PublishedAddsNumberAndTime(t *testing.T) {
	number := int32(5)
	publishedAt := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	baseKeys := []string{"bpmn_xml", "created_at", "id", "is_valid", "module_id", "process_id", "scope", "status"}
	cases := []struct {
		name     string
		version  domain.ModuleVersion
		wantKeys []string
	}{
		{
			name:     "draft",
			version:  domain.ModuleVersion{Status: domain.VersionStatusDraft},
			wantKeys: baseKeys,
		},
		{
			name:     "published",
			version:  domain.ModuleVersion{Status: domain.VersionStatusPublished, VersionNumber: &number, PublishedAt: &publishedAt},
			wantKeys: append([]string{"published_at", "version_number"}, baseKeys...),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stored := tc.version
			stored.ID, stored.ModuleID = testVerID2, testWFID
			stored.Scope, stored.BPMNXML, stored.ProcessID, stored.IsValid = domain.ScopeTenant, "<bpmn/>", "Process_Zzq", true
			var gotTenant, gotModule, gotVersion uuid.UUID
			h := newModuleHandler(&fakeModuleSvc{
				getVersion: func(_ context.Context, tenantID, moduleID, versionID uuid.UUID) (*domain.ModuleVersion, error) {
					gotTenant, gotModule, gotVersion = tenantID, moduleID, versionID
					return &stored, nil
				},
			})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/modules/"+testWFID.String()+"/versions/"+testVerID2.String(), nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			want := append([]string(nil), tc.wantKeys...)
			sort.Strings(want)
			assert.Equal(t, want, w8cSortedKeys(body))
			assert.Equal(t, testVerID2.String(), body["id"])
			assert.Equal(t, string(tc.version.Status), body["status"])
			assert.Equal(t, testTenantID, gotTenant)
			assert.Equal(t, testWFID, gotModule)
			assert.Equal(t, testVerID2, gotVersion)
			if tc.version.VersionNumber != nil {
				assert.Equal(t, float64(5), body["version_number"])
				assert.Equal(t, "2026-05-06T07:08:09Z", body["published_at"])
			}
		})
	}
}
