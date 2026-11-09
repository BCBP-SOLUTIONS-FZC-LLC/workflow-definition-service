package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

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

func versionPath(workflowID, versionID string) string {
	return "/api/v1/workflows/" + url.PathEscape(workflowID) + "/versions/" + url.PathEscape(versionID)
}

func newWiredVersionRouter(t *testing.T) (*mocks.MockWorkflowVersionRepository, *gin.Engine) {
	t.Helper()
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo})
	return vRepo, newRouter(handler.New(handler.Services{Versions: svc}))
}

func TestGetVersion_IdsInAnotherUUIDForm_ResolveToTheSameVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		form func(id uuid.UUID) string
	}{
		{name: "uppercase hex", form: upperHex},
		{name: "braces", form: func(id uuid.UUID) string { return "{" + id.String() + "}" }},
		{name: "urn prefix", form: func(id uuid.UUID) string { return "urn:uuid:" + id.String() }},
		{name: "no hyphens", form: noHyphens},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotWorkflowID, gotVersionID uuid.UUID
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
				get: func(_ context.Context, _, workflowID, versionID uuid.UUID) (*domain.WorkflowVersion, error) {
					gotWorkflowID, gotVersionID = workflowID, versionID
					ver := newPublishedVersion()
					ver.ID, ver.WorkflowID = versionID, workflowID
					return ver, nil
				},
			}, &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, versionPath(tc.form(testWFID), tc.form(testVerID)), nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, testWFID, gotWorkflowID)
			assert.Equal(t, testVerID, gotVersionID)
			var resp map[string]any
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Equal(t, testVerID.String(), resp["id"])
			assert.Equal(t, testWFID.String(), resp["workflow_id"])
		})
	}
}

func upperHex(id uuid.UUID) string {
	b := []byte(id.String())
	for i, c := range b {
		if c >= 'a' && c <= 'f' {
			b[i] = c - 'a' + 'A'
		}
	}
	return string(b)
}

func noHyphens(id uuid.UUID) string {
	out := make([]byte, 0, 32)
	for _, c := range []byte(id.String()) {
		if c != '-' {
			out = append(out, c)
		}
	}
	return string(out)
}

func TestGetVersion_DraftVersion_OmitsPublicationFieldsFromTheBody(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		absent  []string
		present []string
	}{
		{
			name:    "draft without a plan",
			absent:  []string{"version_number", "published_at", "artifact_hash", "compiled_plan_json"},
			present: []string{"id", "workflow_id", "status", "bpmn_xml", "module_refs", "is_valid", "record_version"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			draft := newDraftVersion()
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
				get: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
					return draft, nil
				},
			}, &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, versionPath(testWFID.String(), testVerID.String()), nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var resp map[string]any
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Equal(t, "DRAFT", resp["status"])
			for _, key := range tc.absent {
				assert.NotContains(t, resp, key)
			}
			for _, key := range tc.present {
				assert.Contains(t, resp, key)
			}
		})
	}
}

func TestGetVersion_RealService_VersionOfAnotherTenant_Returns404NotFound(t *testing.T) {
	cases := []struct {
		name       string
		stored     *domain.WorkflowVersion
		repoErr    error
		wantStatus int
	}{
		{name: "repository hides the other tenant's row", repoErr: domain.ErrNotFound, wantStatus: http.StatusNotFound},
		{
			name: "row belongs to a different workflow",
			stored: func() *domain.WorkflowVersion {
				v := newPublishedVersion()
				v.WorkflowID = uuid.New()
				return v
			}(),
			wantStatus: http.StatusNotFound,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vRepo, router := newWiredVersionRouter(t)
			vRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(tc.stored, tc.repoErr)

			w := do(router, req(http.MethodGet, versionPath(testWFID.String(), testVerID.String()), nil))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "NOT_FOUND", prob["code"])
			assert.NotEqual(t, float64(http.StatusForbidden), prob["status"])
		})
	}
}

func TestGetVersion_RealService_ArchivedOrOlderVersion_Returns200WithStoredStatus(t *testing.T) {
	cases := []struct {
		name       string
		status     domain.VersionStatus
		versionNum int32
	}{
		{name: "archived version", status: domain.VersionStatusArchived, versionNum: 1},
		{name: "older published version", status: domain.VersionStatusPublished, versionNum: 1},
		{name: "latest published version", status: domain.VersionStatusPublished, versionNum: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vRepo, router := newWiredVersionRouter(t)
			stored := newPublishedVersion()
			stored.Status = tc.status
			stored.VersionNumber = &tc.versionNum
			vRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(stored, nil)

			w := do(router, req(http.MethodGet, versionPath(testWFID.String(), testVerID.String()), nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var resp map[string]any
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Equal(t, string(tc.status), resp["status"])
			assert.Equal(t, float64(tc.versionNum), resp["version_number"])
			assert.Equal(t, testVerID.String(), resp["id"])
		})
	}
}
