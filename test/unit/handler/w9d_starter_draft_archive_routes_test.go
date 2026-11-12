package handler_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

const (
	w9dTypeNotFound = "https://api.workflow.platform/errors/not-found"
	w9dTypeConflict = "https://api.workflow.platform/errors/conflict"
)

func TestStarterCreate_CompilerFailsWithAnUnclassifiedError_Returns500AndStoresNothing(t *testing.T) {
	const leaked = "w9d zzq compiler crashed at /srv/compiler.go:42"
	cases := []struct {
		name   string
		global bool
		failAt string
	}{
		{name: "validate fails for a tenant starter", failAt: "validate"},
		{name: "validate fails for a global starter", global: true, failAt: "validate"},
		{name: "bundling fails for a tenant starter", failAt: "bundle"},
		{name: "bundling fails for a global starter", global: true, failAt: "bundle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newStarterCreateWiring(t)
			w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
			if tc.failAt == "bundle" {
				w.compiler.EXPECT().BundleModules("<bpmn/>", gomock.Len(0)).Return("", errors.New(leaked))
			} else {
				w.compiler.EXPECT().BundleModules("<bpmn/>", gomock.Len(0)).Return("<bpmn/>", nil)
				w.compiler.EXPECT().Validate(gomock.Any(), "<bpmn/>").Return(nil, errors.New(leaked))
			}
			var r *http.Request
			if tc.global {
				r = platformOperatorReq(http.MethodPost, starterCreatePath, starterCreateBody("global"))
			} else {
				r = adminReq(http.MethodPost, starterCreatePath, starterCreateBody(""))
			}

			rec := do(w.router, r)

			require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
			p := w8dDecodeProblem(t, rec)
			assert.Equal(t, w8dTypeInternal, p.Type)
			assert.Equal(t, http.StatusInternalServerError, p.Status)
			assert.Equal(t, "INTERNAL_ERROR", p.Code)
			assert.Equal(t, w8dDetailInternal, p.Detail)
			assert.Equal(t, starterCreatePath, p.Instance)
			assert.NotContains(t, rec.Body.String(), "compiler.go")
			assert.NotContains(t, rec.Body.String(), "crashed")
		})
	}
}

type w9dFromVersionRig struct {
	starters *mocks.MockStarterTemplateRepository
	versions *mocks.MockWorkflowVersionRepository
	router   *gin.Engine
}

func newW9dFromVersionRig(t *testing.T) *w9dFromVersionRig {
	t.Helper()
	ctrl := gomock.NewController(t)
	r := &w9dFromVersionRig{
		starters: mocks.NewMockStarterTemplateRepository(ctrl),
		versions: mocks.NewMockWorkflowVersionRepository(ctrl),
	}
	svc := service.NewStarterService(service.StarterDeps{
		Starters:       r.starters,
		GlobalStarters: mocks.NewMockStarterTemplateRepository(ctrl),
		Versions:       r.versions,
	})
	r.router = newRouter(handler.New(handler.Services{Starters: svc}))
	return r
}

func w9dFromVersionPath(versionID uuid.UUID) string {
	return sfvPathPrefix + versionID.String()
}

func TestStarterFromWorkflowVersion_SourceLookupFails_ReturnsTheMappedProblemAndStoresNothing(t *testing.T) {
	cases := []struct {
		name       string
		lookupErr  error
		leaked     string
		wantStatus int
		wantType   string
		wantCode   string
		wantDetail string
	}{
		{
			name:       "the version does not exist",
			lookupErr:  fmt.Errorf("get workflow version: %w", domain.ErrNotFound),
			wantStatus: http.StatusNotFound,
			wantType:   w9dTypeNotFound,
			wantCode:   "NOT_FOUND",
			wantDetail: "resource not found",
		},
		{
			name:       "the database is unreachable",
			lookupErr:  fmt.Errorf("get workflow version: %w: %w", domain.ErrUpstreamUnavailable, errors.New("w9d zzq connection refused")),
			leaked:     "zzq connection refused",
			wantStatus: http.StatusServiceUnavailable,
			wantType:   w8dTypeUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantDetail: w8dDetailUnavail,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newW9dFromVersionRig(t)
			versionID := uuid.New()
			rig.versions.EXPECT().GetByID(gomock.Any(), testTenantID, versionID).Return(nil, tc.lookupErr)

			rec := do(rig.router, adminReq(http.MethodPost, w9dFromVersionPath(versionID), map[string]any{"name": "Review"}))

			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			p := w8dDecodeProblem(t, rec)
			assert.Equal(t, tc.wantType, p.Type)
			assert.Equal(t, tc.wantStatus, p.Status)
			assert.Equal(t, tc.wantCode, p.Code)
			assert.Equal(t, tc.wantDetail, p.Detail)
			assert.Equal(t, w9dFromVersionPath(versionID), p.Instance)
			if tc.leaked != "" {
				assert.NotContains(t, rec.Body.String(), tc.leaked)
			}
			assert.NotContains(t, rec.Body.String(), "get workflow version", "the internal error chain is not sent")
		})
	}
}

func TestStarterFromWorkflowVersion_InsertFailsWithAnUnclassifiedError_Returns500WithoutItsText(t *testing.T) {
	rig := newW9dFromVersionRig(t)
	versionID := uuid.New()
	rig.versions.EXPECT().GetByID(gomock.Any(), testTenantID, versionID).
		Return(&domain.WorkflowVersion{ID: versionID, Status: domain.VersionStatusPublished, BPMNXML: "<source/>"}, nil)
	var attempted []*domain.StarterTemplate
	rig.starters.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, st *domain.StarterTemplate) error {
			attempted = append(attempted, st)
			return errors.New("pq: w9d zzq disk full at /var/lib/postgres")
		})

	rec := do(rig.router, adminReq(http.MethodPost, w9dFromVersionPath(versionID), map[string]any{"name": "Review"}))

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	p := w8dDecodeProblem(t, rec)
	assert.Equal(t, w8dTypeInternal, p.Type)
	assert.Equal(t, http.StatusInternalServerError, p.Status)
	assert.Equal(t, "INTERNAL_ERROR", p.Code)
	assert.Equal(t, w8dDetailInternal, p.Detail)
	assert.Equal(t, w9dFromVersionPath(versionID), p.Instance)
	assert.NotContains(t, rec.Body.String(), "disk full")
	require.Len(t, attempted, 1, "the insert was attempted once")
	require.NotNil(t, attempted[0].SourceWorkflowVersionID)
	assert.Equal(t, versionID, *attempted[0].SourceWorkflowVersionID)
}

func TestStarterFromWorkflowVersion_PublishedOrArchivedSource_Returns201WithTheStarterOfTheCaller(t *testing.T) {
	cases := []struct {
		name   string
		status domain.VersionStatus
	}{
		{name: "published source", status: domain.VersionStatusPublished},
		{name: "archived source", status: domain.VersionStatusArchived},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newW9dFromVersionRig(t)
			versionID := uuid.New()
			rig.versions.EXPECT().GetByID(gomock.Any(), testTenantID, versionID).Return(&domain.WorkflowVersion{
				ID: versionID, TenantID: testTenantID, Status: tc.status, BPMNXML: "<w9d-source/>",
				CreatedByUserID: uuid.New(),
			}, nil)
			var stored []*domain.StarterTemplate
			rig.starters.EXPECT().Create(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, st *domain.StarterTemplate) error {
					stored = append(stored, st)
					return nil
				})

			rec := do(rig.router, adminReq(http.MethodPost, w9dFromVersionPath(versionID),
				map[string]any{"name": "Review", "description": "From a version", "category": "ops"}))

			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			require.Len(t, stored, 1)
			row := stored[0]
			require.NotNil(t, row.TenantID)
			assert.Equal(t, testTenantID, *row.TenantID)
			require.NotNil(t, row.CreatedByUserID)
			assert.Equal(t, testUserID, *row.CreatedByUserID)
			assert.Equal(t, domain.ScopeTenant, row.Scope)
			assert.Equal(t, "<w9d-source/>", row.BPMNXML)
			require.NotNil(t, row.SourceWorkflowVersionID)
			assert.Equal(t, versionID, *row.SourceWorkflowVersionID)

			body := w8dBody(t, rec)
			assert.Equal(t, row.ID.String(), body["id"])
			assert.Equal(t, testTenantID.String(), body["tenant_id"])
			assert.Equal(t, "tenant", body["scope"])
			assert.Equal(t, "Review", body["name"])
			assert.Equal(t, "From a version", body["description"])
			assert.Equal(t, "ops", body["category"])
			assert.Equal(t, "<w9d-source/>", body["bpmn_xml"])
			assert.Equal(t, versionID.String(), body["source_workflow_version_id"])
		})
	}
}

func TestInitDraft_WorkflowWithoutAnActiveVersion_Returns409AndCreatesNoDraft(t *testing.T) {
	wfRepo, _, router := newWiredDraftRouter(t)
	wfRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).
		Return(&domain.Workflow{ID: testWFID, TenantID: testTenantID, ActiveVersionID: nil}, nil)
	path := "/api/v1/workflows/" + testWFID.String() + "/draft"

	rec := do(router, adminReq(http.MethodPost, path, nil))

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	p := w8dDecodeProblem(t, rec)
	assert.Equal(t, w9dTypeConflict, p.Type)
	assert.Equal(t, http.StatusConflict, p.Status)
	assert.Equal(t, "NO_ACTIVE_VERSION", p.Code)
	assert.Equal(t, "no active version", p.Detail)
	assert.Equal(t, path, p.Instance)
}

func TestSaveDraft_WorkflowMetadataWriteFails_ReturnsTheMappedProblemAndSavesNoDraftRow(t *testing.T) {
	cases := []struct {
		name       string
		failure    error
		leaked     string
		wantStatus int
		wantType   string
		wantCode   string
		wantDetail string
	}{
		{
			name:       "database unreachable",
			failure:    fmt.Errorf("%w: %w", domain.ErrUpstreamUnavailable, errors.New("w9d zzq connection refused")),
			leaked:     "zzq connection refused",
			wantStatus: http.StatusServiceUnavailable,
			wantType:   w8dTypeUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantDetail: w8dDetailUnavail,
		},
		{
			name:       "other database error",
			failure:    errors.New("pq: w9d zzq deadlock detected"),
			leaked:     "zzq deadlock",
			wantStatus: http.StatusInternalServerError,
			wantType:   w8dTypeInternal,
			wantCode:   "INTERNAL_ERROR",
			wantDetail: w8dDetailInternal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wfRepo, vRepo, router := newWiredDraftRouter(t)
			vRepo.EXPECT().GetDraft(gomock.Any(), testTenantID, testWFID).
				Return(&domain.WorkflowVersion{ID: uuid.New(), WorkflowID: testWFID, RecordVersion: 3}, nil)
			wfRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).
				Return(&domain.Workflow{ID: testWFID, Name: "old", Description: "old description"}, nil)
			var written []string
			wfRepo.EXPECT().UpdateMetadata(gomock.Any(), testTenantID, testWFID, gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _, _ uuid.UUID, name, description string) error {
					written = append(written, name+"|"+description)
					return tc.failure
				})
			path := "/api/v1/workflows/" + testWFID.String() + "/draft"

			rec := do(router, adminReq(http.MethodPut, path, map[string]any{"name": "renamed", "record_version": 3}))

			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			p := w8dDecodeProblem(t, rec)
			assert.Equal(t, tc.wantType, p.Type)
			assert.Equal(t, tc.wantStatus, p.Status)
			assert.Equal(t, tc.wantCode, p.Code)
			assert.Equal(t, tc.wantDetail, p.Detail)
			assert.Equal(t, path, p.Instance)
			assert.NotContains(t, rec.Body.String(), tc.leaked)
			assert.NotContains(t, rec.Body.String(), "update workflow metadata")
			assert.Equal(t, []string{"renamed|old description"}, written, "one metadata write with the description carried over")
		})
	}
}

func TestArchiveWorkflow_UnknownWorkflow_Returns404AndChangesNothing(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor: mocks.NewMockTransactor(ctrl),
		Workflows:  wfRepo,
		Versions:   mocks.NewMockWorkflowVersionRepository(ctrl),
		Execution:  mocks.NewMockExecutionService(ctrl),
		Cache:      mocks.NewMockCacheStore(ctrl),
	})
	router := newRouter(handler.New(handler.Services{Workflows: svc}))
	wfRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).
		Return(nil, fmt.Errorf("get workflow: %w", domain.ErrNotFound))
	path := "/api/v1/workflows/" + testWFID.String() + "/archive"

	rec := do(router, adminReq(http.MethodPost, path, nil))

	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	p := w8dDecodeProblem(t, rec)
	assert.Equal(t, w9dTypeNotFound, p.Type)
	assert.Equal(t, http.StatusNotFound, p.Status)
	assert.Equal(t, "NOT_FOUND", p.Code)
	assert.Equal(t, "resource not found", p.Detail)
	assert.Equal(t, path, p.Instance)
	assert.NotContains(t, rec.Body.String(), "get workflow")
}
