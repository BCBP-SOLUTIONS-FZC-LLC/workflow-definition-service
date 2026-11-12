package handler_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const w8dPublishedNumber = int32(7)

func w8dPublishPath() string {
	return "/api/v1/modules/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
}

func w8dArchivePath() string {
	return "/api/v1/modules/" + testWFID.String()
}

func (w *w8dModules) expectPublishable() *w8dPublishCapture {
	c := &w8dPublishCapture{}
	w.expectModule(nil)
	w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(&domain.ModuleVersion{
		ID: testVerID, ModuleID: testWFID, Scope: domain.ScopeTenant, Status: domain.VersionStatusDraft,
		BPMNXML: "<draft/>", ProcessID: w8dProcessID, IsValid: true,
	}, nil)
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<draft/>").Return(nil, nil, nil)
	w.compiler.EXPECT().BundleModules("<draft/>", gomock.Any()).Return("<bundled/>", nil)
	w.compiler.EXPECT().ValidateModule(gomock.Any(), "<bundled/>", w8dProcessID, false).Return(nil, nil)
	next := w.versions.EXPECT().NextVersionNumber(gomock.Any(), testWFID).DoAndReturn(
		func(context.Context, uuid.UUID) (int32, error) {
			c.numberInTx = w.tx.inside()
			return w8dPublishedNumber, nil
		})
	publish := w.versions.EXPECT().Publish(gomock.Any(), testTenantID, testVerID, w8dPublishedNumber).DoAndReturn(
		func(context.Context, uuid.UUID, uuid.UUID, int32) error {
			c.publishInTx = w.tx.inside()
			c.publishes++
			return nil
		}).After(next)
	w.modules.EXPECT().UpdateActiveVersion(gomock.Any(), testWFID, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ uuid.UUID, versionID *uuid.UUID) error {
			c.pointerInTx = w.tx.inside()
			c.pointer = versionID
			return nil
		}).After(publish)
	return c
}

type w8dPublishCapture struct {
	numberInTx  bool
	publishInTx bool
	pointerInTx bool
	publishes   int
	pointer     *uuid.UUID
}

func TestPublishModuleVersion_ValidDraft_Returns200WithTheNumberAndMovesThePointerInOneTransaction(t *testing.T) {
	t.Parallel()
	w := w8dNewModules(t, nil, nil)
	c := w.expectPublishable()

	rec := do(w.router, adminReq(http.MethodPost, w8dPublishPath(), nil))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := w8dBody(t, rec)
	assert.Equal(t, testWFID.String(), body["module_id"])
	assert.Equal(t, testVerID.String(), body["version_id"])
	assert.Equal(t, "PUBLISHED", body["status"])
	assert.EqualValues(t, w8dPublishedNumber, body["version_number"], "the number comes from the repository")
	assert.Equal(t, "Module version published", body["message"])
	assert.True(t, c.numberInTx && c.publishInTx && c.pointerInTx, "numbering, publish and pointer share one transaction")
	assert.Equal(t, int32(1), w.tx.runs.Load())
	require.NotNil(t, c.pointer, "the active pointer is set")
	assert.Equal(t, testVerID, *c.pointer)
	logged := w.log.infoLines("module version published")
	require.Len(t, logged, 1)
	assert.Equal(t, testTenantID.String(), logged[0].fields["tenant_id"])
	assert.Equal(t, testUserID.String(), logged[0].fields["user_id"])
	assert.Equal(t, testWFID.String(), logged[0].fields["module_id"])
	assert.Equal(t, testVerID.String(), logged[0].fields["version_id"])
}

func TestPublishModuleVersion_SameIdempotencyKey_ReplaysTheFirstAnswerWithoutPublishingAgain(t *testing.T) {
	t.Parallel()
	w := w8dNewModules(t, nil, newMemCache())
	c := w.expectPublishable()
	send := func() map[string]any {
		r := adminReq(http.MethodPost, w8dPublishPath(), nil)
		r.Header.Set("Idempotency-Key", "publish-module-version-key")
		rec := do(w.router, r)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return w8dBody(t, rec)
	}

	first := send()
	second := send()

	assert.Equal(t, first, second, "the replay is the first answer")
	assert.EqualValues(t, w8dPublishedNumber, second["version_number"], "the version number is unchanged")
	assert.Equal(t, 1, c.publishes, "the version is published once")
	assert.Equal(t, int32(1), w.tx.runs.Load(), "the second request never opens a transaction")
}

func TestArchiveModule_PlatformOperatorWithoutAdminRole_Returns403WithoutLookup(t *testing.T) {
	t.Parallel()
	var lookups, archives int
	h := newModuleHandler(&fakeModuleSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			lookups++
			return &domain.Module{ID: testWFID, Scope: domain.ScopeGlobal}, nil, nil
		},
		archive: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
			archives++
			return nil
		},
	})
	r := req(http.MethodDelete, w8dArchivePath(), nil)
	r.Header.Set("x-tenant-roles", "platform_operator")

	rec := do(newRouter(h), r)

	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	p := w8dDecodeProblem(t, rec)
	assert.Equal(t, w8dTypeForbidden, p.Type)
	assert.Equal(t, http.StatusForbidden, p.Status)
	assert.Equal(t, "FORBIDDEN", p.Code)
	assert.Equal(t, "caller lacks tenant_admin/tenant_owner role", p.Detail)
	assert.Equal(t, w8dArchivePath(), p.Instance)
	assert.Zero(t, lookups, "the admin check runs before any module lookup")
	assert.Zero(t, archives)
}

func TestArchiveModule_ActiveVersion_Returns200AndArchivesItAndClearsThePointerInOneTransaction(t *testing.T) {
	t.Parallel()
	activeID := uuid.New()
	w := w8dNewModules(t, nil, nil)
	w.expectModule(&activeID)
	w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, activeID).
		Return(&domain.ModuleVersion{ID: activeID, ModuleID: testWFID}, nil).AnyTimes()
	var usagesInTx, draftsInTx, archiveInTx, pointerInTx bool
	w.workflowVersions.EXPECT().ListModuleUsages(gomock.Any(), testTenantID, testWFID).DoAndReturn(
		func(context.Context, uuid.UUID, uuid.UUID) ([]*domain.ModuleUsage, error) {
			usagesInTx = w.tx.inside()
			return nil, nil
		})
	w.workflowVersions.EXPECT().ListDraftSources(gomock.Any(), testTenantID).DoAndReturn(
		func(context.Context, uuid.UUID) ([]*domain.DraftSource, error) {
			draftsInTx = w.tx.inside()
			return nil, nil
		})
	archive := w.versions.EXPECT().Archive(gomock.Any(), testTenantID, activeID).DoAndReturn(
		func(context.Context, uuid.UUID, uuid.UUID) error {
			archiveInTx = w.tx.inside()
			return nil
		})
	var cleared bool
	w.modules.EXPECT().UpdateActiveVersion(gomock.Any(), testWFID, gomock.Nil()).DoAndReturn(
		func(context.Context, uuid.UUID, *uuid.UUID) error {
			pointerInTx = w.tx.inside()
			cleared = true
			return nil
		}).After(archive)

	rec := do(w.router, adminReq(http.MethodDelete, w8dArchivePath(), nil))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := w8dBody(t, rec)
	assert.Equal(t, testWFID.String(), body["module_id"])
	assert.Equal(t, "ARCHIVED", body["status"])
	assert.Equal(t, "Module archived successfully", body["message"])
	assert.True(t, usagesInTx && draftsInTx && archiveInTx && pointerInTx, "the in-use check, the archive and the pointer clear share one transaction")
	assert.Equal(t, int32(1), w.tx.runs.Load())
	assert.True(t, cleared, "the active pointer is cleared")
	logged := w.log.infoLines("module archived")
	require.Len(t, logged, 1)
	assert.Equal(t, testTenantID.String(), logged[0].fields["tenant_id"])
	assert.Equal(t, testUserID.String(), logged[0].fields["user_id"])
	assert.Equal(t, testWFID.String(), logged[0].fields["module_id"])
	assert.Equal(t, activeID.String(), logged[0].fields["version_id"])
}

func TestArchiveModule_UnexpectedRepositoryError_Returns500WithoutTheInternalText(t *testing.T) {
	t.Parallel()
	const leaked = "pq: relation workflow_module is corrupt"
	cases := []struct {
		name  string
		setup func(w *w8dModules, activeID uuid.UUID)
	}{
		{
			name: "the in-use check fails",
			setup: func(w *w8dModules, _ uuid.UUID) {
				w.workflowVersions.EXPECT().ListModuleUsages(gomock.Any(), testTenantID, testWFID).Return(nil, errors.New(leaked))
			},
		},
		{
			name: "the version archive fails",
			setup: func(w *w8dModules, activeID uuid.UUID) {
				w.workflowVersions.EXPECT().ListModuleUsages(gomock.Any(), testTenantID, testWFID).Return(nil, nil)
				w.workflowVersions.EXPECT().ListDraftSources(gomock.Any(), testTenantID).Return(nil, nil)
				w.versions.EXPECT().Archive(gomock.Any(), testTenantID, activeID).Return(errors.New(leaked))
			},
		},
		{
			name: "the pointer clear fails",
			setup: func(w *w8dModules, activeID uuid.UUID) {
				w.workflowVersions.EXPECT().ListModuleUsages(gomock.Any(), testTenantID, testWFID).Return(nil, nil)
				w.workflowVersions.EXPECT().ListDraftSources(gomock.Any(), testTenantID).Return(nil, nil)
				w.versions.EXPECT().Archive(gomock.Any(), testTenantID, activeID).Return(nil)
				w.modules.EXPECT().UpdateActiveVersion(gomock.Any(), testWFID, gomock.Nil()).Return(errors.New(leaked))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			activeID := uuid.New()
			w := w8dNewModules(t, nil, nil)
			w.expectModule(&activeID)
			w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, activeID).
				Return(&domain.ModuleVersion{ID: activeID, ModuleID: testWFID}, nil).AnyTimes()
			tc.setup(w, activeID)

			rec := do(w.router, adminReq(http.MethodDelete, w8dArchivePath(), nil))

			require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
			p := w8dDecodeProblem(t, rec)
			assert.Equal(t, w8dTypeInternal, p.Type)
			assert.Equal(t, http.StatusInternalServerError, p.Status)
			assert.Equal(t, "INTERNAL_ERROR", p.Code)
			assert.Equal(t, w8dDetailInternal, p.Detail)
			assert.Equal(t, w8dArchivePath(), p.Instance)
			assert.NotContains(t, rec.Body.String(), leaked)
			assert.Empty(t, w.log.infoLines("module archived"))
			logged := w.log.errorLines("request failed")
			require.Len(t, logged, 1)
			assert.EqualValues(t, http.StatusInternalServerError, logged[0].fields["status"])
			assert.Equal(t, testTenantID.String(), logged[0].fields["tenant_id"])
			assert.Equal(t, testUserID.String(), logged[0].fields["user_id"])
			assert.Equal(t, w8dArchivePath(), logged[0].fields["path"])
		})
	}
}
