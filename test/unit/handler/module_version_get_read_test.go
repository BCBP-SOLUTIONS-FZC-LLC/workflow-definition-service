package handler_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func moduleVersionGetPath(moduleID, versionID string) string {
	return "/api/v1/modules/" + moduleID + "/versions/" + versionID
}

func newModuleVersionGetRouter(t *testing.T, logger *fakeLogger) (*mocks.MockModuleVersionRepository, *gin.Engine) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockModuleVersionRepository(ctrl)
	svc := service.NewModuleService(service.ModuleDeps{Versions: repo})
	services := handler.Services{Modules: svc}
	if logger != nil {
		services.Log = logger
	}
	return repo, newRouter(handler.New(services))
}

func moduleVersionGetStrip(id uuid.UUID) string {
	return strings.ReplaceAll(id.String(), "-", "")
}

func moduleVersionGetDo(router *gin.Engine, moduleID, versionID string) *httptest.ResponseRecorder {
	return do(router, req(http.MethodGet, moduleVersionGetPath(moduleID, versionID), nil))
}

func TestGetModuleVersion_MalformedPathIDs_Returns400WithoutLookup(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		moduleID   string
		versionID  string
		wantDetail string
	}{
		{"module id is not a UUID", "not-a-uuid", testVerID.String(), "invalid id: not a valid UUID"},
		{"version id is not a UUID", testWFID.String(), "not-a-uuid", "invalid version_id: not a valid UUID"},
		{"both malformed reports the module id", "not-a-uuid", "also-not", "invalid id: not a valid UUID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, router := newModuleVersionGetRouter(t, nil)

			w := moduleVersionGetDo(router, tc.moduleID, tc.versionID)

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, tc.wantDetail, prob["detail"])
		})
	}
}

func TestGetModuleVersion_VersionStatus_ShapesTheBody(t *testing.T) {
	t.Parallel()

	number := int32(3)
	publishedAt := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	cases := []struct {
		name           string
		version        domain.ModuleVersion
		wantStatus     string
		wantNumber     any
		wantPublished  any
		wantKeysAbsent []string
	}{
		{
			name: "draft omits the publication keys",
			version: domain.ModuleVersion{
				Status: domain.VersionStatusDraft,
			},
			wantStatus:     "DRAFT",
			wantKeysAbsent: []string{"version_number", "published_at"},
		},
		{
			name: "published carries number and time",
			version: domain.ModuleVersion{
				Status: domain.VersionStatusPublished, VersionNumber: &number, PublishedAt: &publishedAt,
			},
			wantStatus:    "PUBLISHED",
			wantNumber:    float64(3),
			wantPublished: "2026-03-04T05:06:07Z",
		},
		{
			name: "archived is readable and keeps number and time",
			version: domain.ModuleVersion{
				Status: domain.VersionStatusArchived, VersionNumber: &number, PublishedAt: &publishedAt,
			},
			wantStatus:    "ARCHIVED",
			wantNumber:    float64(3),
			wantPublished: "2026-03-04T05:06:07Z",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stored := tc.version
			stored.ID, stored.ModuleID = testVerID, testWFID
			stored.Scope, stored.BPMNXML, stored.ProcessID, stored.IsValid = domain.ScopeGlobal, "<bpmn/>", "Process_1", true
			repo, router := newModuleVersionGetRouter(t, nil)
			repo.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(&stored, nil)

			w := moduleVersionGetDo(router, testWFID.String(), testVerID.String())

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, testVerID.String(), body["id"])
			assert.Equal(t, testWFID.String(), body["module_id"])
			assert.Equal(t, "global", body["scope"])
			assert.Equal(t, tc.wantStatus, body["status"])
			assert.Equal(t, "<bpmn/>", body["bpmn_xml"])
			assert.Equal(t, "Process_1", body["process_id"])
			assert.Equal(t, true, body["is_valid"])
			if tc.wantNumber != nil {
				assert.Equal(t, tc.wantNumber, body["version_number"])
				assert.Equal(t, tc.wantPublished, body["published_at"])
			}
			for _, key := range tc.wantKeysAbsent {
				assert.NotContains(t, body, key)
			}
		})
	}
}

func TestGetModuleVersion_IDWithoutHyphens_ResolvesToTheCanonicalIDs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		form func(id uuid.UUID) string
	}{
		{"no hyphens", moduleVersionGetStrip},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repo, router := newModuleVersionGetRouter(t, nil)
			repo.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).
				Return(&domain.ModuleVersion{ID: testVerID, ModuleID: testWFID, Status: domain.VersionStatusDraft}, nil)

			w := moduleVersionGetDo(router, tc.form(testWFID), tc.form(testVerID))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, testVerID.String(), body["id"])
			assert.Equal(t, testWFID.String(), body["module_id"])
		})
	}
}

func TestGetModuleVersion_LookupFailure_MapsToProblem(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		repoErr    error
		wantStatus int
		wantCode   string
		wantDetail string
		wantLogged bool
	}{
		{
			name:       "unknown version",
			repoErr:    domain.ErrNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   "NOT_FOUND",
		},
		{
			name:       "database unreachable",
			repoErr:    fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantDetail: "an upstream dependency is temporarily unavailable",
			wantLogged: true,
		},
		{
			name:       "unclassified failure",
			repoErr:    errors.New("scan row: unexpected column type"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantDetail: "an unexpected error occurred",
			wantLogged: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger := &fakeLogger{}
			repo, router := newModuleVersionGetRouter(t, logger)
			repo.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(nil, tc.repoErr)

			w := moduleVersionGetDo(router, testWFID.String(), testVerID.String())

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, tc.wantCode, prob["code"])
			if tc.wantDetail != "" {
				assert.Equal(t, tc.wantDetail, prob["detail"])
			}
			if tc.wantLogged {
				assert.Equal(t, []string{"request failed"}, logger.errorCalls)
			} else {
				assert.Empty(t, logger.errorCalls)
			}
		})
	}
}
