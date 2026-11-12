package service_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

type w9dLogLine struct {
	msg    string
	fields map[string]any
}

type w9dLog struct {
	testkit.FakeLogger
	mu    sync.Mutex
	infos []w9dLogLine
	warns []w9dLogLine
}

func (l *w9dLog) Info(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infos = append(l.infos, w9dLogLine{msg: msg, fields: fields})
}

func (l *w9dLog) Warn(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, w9dLogLine{msg: msg, fields: fields})
}

func (l *w9dLog) warnLines() []w9dLogLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]w9dLogLine(nil), l.warns...)
}

func (l *w9dLog) infoLines() []w9dLogLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]w9dLogLine(nil), l.infos...)
}

func w9dRequireUUIDv7(t *testing.T, id uuid.UUID) {
	t.Helper()
	require.NotEqual(t, uuid.Nil, id)
	assert.Equal(t, uuid.Version(7), id.Version(), "the id is a time-ordered UUID")
	assert.Equal(t, uuid.RFC4122, id.Variant())
}

func TestStarterService_Create_CompilerFails_ReturnsTheCauseAndStoresNothing(t *testing.T) {
	t.Parallel()
	validateCause := errors.New("w9d zzq validate refused")
	bundleCause := errors.New("w9d zzq bundle refused")
	cases := []struct {
		name       string
		scope      domain.CatalogScope
		failAt     string
		cause      error
		wantPrefix string
	}{
		{name: "validate fails for a tenant starter", scope: domain.ScopeTenant, failAt: "validate", cause: validateCause, wantPrefix: "validate bpmn: "},
		{name: "validate fails for a global starter", scope: domain.ScopeGlobal, failAt: "validate", cause: validateCause, wantPrefix: "validate bpmn: "},
		{name: "bundling fails for a tenant starter", scope: domain.ScopeTenant, failAt: "bundle", cause: bundleCause, wantPrefix: "bundle modules: "},
		{name: "bundling fails for a global starter", scope: domain.ScopeGlobal, failAt: "bundle", cause: bundleCause, wantPrefix: "bundle modules: "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			svc, m := newStarterService(ctrl)
			m.compiler.EXPECT().ModuleCalls(gomock.Any(), "<w9d-main/>").Return(nil, nil, nil)
			if tc.failAt == "bundle" {
				m.compiler.EXPECT().BundleModules("<w9d-main/>", gomock.Len(0)).Return("", tc.cause)
			} else {
				m.compiler.EXPECT().BundleModules("<w9d-main/>", gomock.Len(0)).Return("<w9d-bundled/>", nil)
				m.compiler.EXPECT().Validate(gomock.Any(), "<w9d-bundled/>").Return(nil, tc.cause)
			}

			st, err := svc.Create(context.Background(), uuid.New(), uuid.New(), service.CreateStarterReq{
				Name: "w9d", BPMNXML: "<w9d-main/>", Scope: tc.scope,
			})

			require.Error(t, err)
			assert.Nil(t, st)
			assert.ErrorIs(t, err, tc.cause)
			assert.True(t, strings.HasPrefix(err.Error(), tc.wantPrefix), "err = %q", err)
			var vfe *domain.ValidationFailedError
			assert.False(t, errors.As(err, &vfe), "a compiler failure is not a validation finding")
		})
	}
}

func TestStarterService_Create_StoresTheStarterOfTheCallerOnlyInTheMatchingRepository(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		scope      domain.CatalogScope
		wantScope  domain.CatalogScope
		wantTenant bool
	}{
		{name: "scope omitted defaults to the caller tenant", scope: "", wantScope: domain.ScopeTenant, wantTenant: true},
		{name: "explicit tenant scope", scope: domain.ScopeTenant, wantScope: domain.ScopeTenant, wantTenant: true},
		{name: "global scope has no owning tenant", scope: domain.ScopeGlobal, wantScope: domain.ScopeGlobal, wantTenant: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			svc, m := newStarterService(ctrl)
			expectNoLibraryCalls(m.compiler)
			m.compiler.EXPECT().Validate(gomock.Any(), "<w9d-main/>").Return(nil, nil)
			var stored []*domain.StarterTemplate
			capture := func(_ context.Context, st *domain.StarterTemplate) error {
				stored = append(stored, st)
				return nil
			}
			if tc.wantTenant {
				m.starters.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(capture)
			} else {
				m.globalStarters.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(capture)
			}
			tenantID, userID := uuid.New(), uuid.New()

			returned, err := svc.Create(context.Background(), tenantID, userID, service.CreateStarterReq{
				Name: "w9d name", Description: "w9d description", Category: "w9d-category",
				BPMNXML: "<w9d-main/>", Scope: tc.scope,
			})

			require.NoError(t, err)
			require.Len(t, stored, 1, "exactly one row is written")
			row := stored[0]
			assert.Same(t, row, returned, "the row written is the row answered")
			assert.Equal(t, tc.wantScope, row.Scope)
			if tc.wantTenant {
				require.NotNil(t, row.TenantID)
				assert.Equal(t, tenantID, *row.TenantID, "the row is owned by the caller tenant")
			} else {
				assert.Nil(t, row.TenantID)
			}
			require.NotNil(t, row.CreatedByUserID)
			assert.Equal(t, userID, *row.CreatedByUserID)
			assert.Nil(t, row.SourceWorkflowVersionID)
			assert.Equal(t, "<w9d-main/>", row.BPMNXML)
			assert.Equal(t, "w9d name", row.Name)
			assert.Equal(t, "w9d description", row.Description)
			assert.Equal(t, "w9d-category", row.Category)
			w9dRequireUUIDv7(t, row.ID)
		})
	}
}

func TestStarterService_CreateFromWorkflowVersion_StoresATenantStarterOwnedByTheCaller(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)
	tenantID, userID, sourceID := uuid.New(), uuid.New(), uuid.New()
	sourceTenant := uuid.New()
	m.versions.EXPECT().GetByID(gomock.Any(), tenantID, sourceID).Return(&domain.WorkflowVersion{
		ID: sourceID, TenantID: sourceTenant, Status: domain.VersionStatusPublished, BPMNXML: "<w9d-source/>",
		CreatedByUserID: uuid.New(),
	}, nil)
	var stored []*domain.StarterTemplate
	m.starters.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, st *domain.StarterTemplate) error {
			stored = append(stored, st)
			return nil
		})

	returned, err := svc.CreateFromWorkflowVersion(context.Background(), tenantID, userID, sourceID,
		service.CreateFromWorkflowVersionReq{Name: "w9d name", Description: "w9d description", Category: "w9d-category"})

	require.NoError(t, err)
	require.Len(t, stored, 1)
	row := stored[0]
	assert.Same(t, row, returned)
	assert.Equal(t, domain.ScopeTenant, row.Scope)
	require.NotNil(t, row.TenantID)
	assert.Equal(t, tenantID, *row.TenantID, "owned by the caller tenant, not by the tenant recorded on the source")
	require.NotNil(t, row.CreatedByUserID)
	assert.Equal(t, userID, *row.CreatedByUserID, "created by the caller, not by the author of the source")
	require.NotNil(t, row.SourceWorkflowVersionID)
	assert.Equal(t, sourceID, *row.SourceWorkflowVersionID)
	assert.Equal(t, "<w9d-source/>", row.BPMNXML)
	assert.Equal(t, "w9d name", row.Name)
	assert.Equal(t, "w9d description", row.Description)
	assert.Equal(t, "w9d-category", row.Category)
	w9dRequireUUIDv7(t, row.ID)
}

func TestStarterService_CreateFromWorkflowVersion_CalledTwiceForOneSource_StoresTwoStartersWithDistinctIds(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc, m := newStarterService(ctrl)
	tenantID, userID, sourceID := uuid.New(), uuid.New(), uuid.New()
	m.versions.EXPECT().GetByID(gomock.Any(), tenantID, sourceID).
		Return(&domain.WorkflowVersion{ID: sourceID, Status: domain.VersionStatusArchived, BPMNXML: "<w9d-source/>"}, nil).Times(2)
	var stored []*domain.StarterTemplate
	m.starters.EXPECT().Create(gomock.Any(), gomock.Any()).Times(2).
		DoAndReturn(func(_ context.Context, st *domain.StarterTemplate) error {
			stored = append(stored, st)
			return nil
		})

	first, err := svc.CreateFromWorkflowVersion(context.Background(), tenantID, userID, sourceID,
		service.CreateFromWorkflowVersionReq{Name: "w9d same name"})
	require.NoError(t, err)
	second, err := svc.CreateFromWorkflowVersion(context.Background(), tenantID, userID, sourceID,
		service.CreateFromWorkflowVersionReq{Name: "w9d same name"})
	require.NoError(t, err)

	require.Len(t, stored, 2, "a repeated call writes a second row; nothing deduplicates it")
	assert.NotEqual(t, first.ID, second.ID)
	w9dRequireUUIDv7(t, first.ID)
	w9dRequireUUIDv7(t, second.ID)
	for _, row := range stored {
		require.NotNil(t, row.SourceWorkflowVersionID)
		assert.Equal(t, sourceID, *row.SourceWorkflowVersionID)
	}
}

func TestStarterService_CreateFromWorkflowVersion_LookupOrInsertFails_ReturnsTheCause(t *testing.T) {
	t.Parallel()
	outage := errors.New("w9d zzq connection refused")
	disk := errors.New("w9d zzq disk full")
	cases := []struct {
		name       string
		failAt     string
		cause      error
		wrapAs     error
		wantPrefix string
	}{
		{name: "lookup outage", failAt: "lookup", cause: errors.Join(domain.ErrUpstreamUnavailable, outage), wrapAs: domain.ErrUpstreamUnavailable, wantPrefix: "get source workflow version: "},
		{name: "lookup not found", failAt: "lookup", cause: domain.ErrNotFound, wrapAs: domain.ErrNotFound, wantPrefix: "get source workflow version: "},
		{name: "insert refused", failAt: "insert", cause: disk, wrapAs: disk, wantPrefix: "create starter from workflow version: "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			svc, m := newStarterService(ctrl)
			tenantID, userID, sourceID := uuid.New(), uuid.New(), uuid.New()
			if tc.failAt == "lookup" {
				m.versions.EXPECT().GetByID(gomock.Any(), tenantID, sourceID).Return(nil, tc.cause)
			} else {
				m.versions.EXPECT().GetByID(gomock.Any(), tenantID, sourceID).
					Return(&domain.WorkflowVersion{ID: sourceID, Status: domain.VersionStatusPublished, BPMNXML: "<w9d/>"}, nil)
				m.starters.EXPECT().Create(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, _ *domain.StarterTemplate) error { return tc.cause })
			}

			st, err := svc.CreateFromWorkflowVersion(context.Background(), tenantID, userID, sourceID,
				service.CreateFromWorkflowVersionReq{Name: "w9d"})

			require.Error(t, err)
			assert.Nil(t, st)
			assert.ErrorIs(t, err, tc.wrapAs)
			assert.True(t, strings.HasPrefix(err.Error(), tc.wantPrefix), "err = %q", err)
		})
	}
}

type w9dArchiveRig struct {
	tx    *mocks.MockTransactor
	wf    *mocks.MockWorkflowRepository
	vers  *mocks.MockWorkflowVersionRepository
	exec  *mocks.MockExecutionService
	cache *mocks.MockCacheStore
	log   *w9dLog
	svc   *service.WorkflowService
}

func newW9dArchiveRig(ctrl *gomock.Controller) *w9dArchiveRig {
	r := &w9dArchiveRig{
		tx:    mocks.NewMockTransactor(ctrl),
		wf:    mocks.NewMockWorkflowRepository(ctrl),
		vers:  mocks.NewMockWorkflowVersionRepository(ctrl),
		exec:  mocks.NewMockExecutionService(ctrl),
		cache: mocks.NewMockCacheStore(ctrl),
		log:   &w9dLog{},
	}
	r.svc = service.NewWorkflowService(service.WorkflowDeps{
		Transactor: r.tx, Workflows: r.wf, Versions: r.vers, Execution: r.exec, Cache: r.cache, Log: r.log,
	})
	return r
}

func (r *w9dArchiveRig) runTxInline() {
	r.tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
}

func TestWorkflowService_Archive_Succeeds_EvictsTheCompiledPlanOfTheVersionItArchived(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	r := newW9dArchiveRig(ctrl)
	tenantID, userID, wfID := uuid.New(), uuid.New(), uuid.New()
	seenActive, archivedActive := uuid.New(), uuid.New()

	gomock.InOrder(
		r.wf.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ID: wfID, ActiveVersionID: &seenActive}, nil),
		r.exec.EXPECT().CheckActiveInstances(gomock.Any(), tenantID, wfID).Return(false, int32(0), nil),
		r.wf.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ID: wfID, ActiveVersionID: &archivedActive}, nil),
		r.vers.EXPECT().Archive(gomock.Any(), tenantID, archivedActive).Return(nil),
		r.wf.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, (*uuid.UUID)(nil)).Return(nil),
	)
	r.runTxInline()
	var evicted []string
	r.cache.EXPECT().Del(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, keys ...string) error {
		evicted = append(evicted, keys...)
		return nil
	})

	require.NoError(t, r.svc.Archive(context.Background(), tenantID, userID, wfID))

	require.Len(t, evicted, 1, "one key is evicted")
	assert.Equal(t, domain.CompiledPlanCacheKey(tenantID, archivedActive), evicted[0],
		"the key is the one the plan read path uses, for the version that was archived")
	assert.Contains(t, evicted[0], tenantID.String())
	assert.Contains(t, evicted[0], archivedActive.String())
	assert.NotContains(t, evicted[0], seenActive.String(), "the version seen before the transaction is not the one archived")
	assert.Empty(t, r.log.warnLines(), "a successful eviction logs no warning")
}

func TestWorkflowService_Archive_Fails_EvictsNothing(t *testing.T) {
	t.Parallel()
	activeID := uuid.New()
	withActive := &domain.Workflow{ActiveVersionID: &activeID}
	txFailure := errors.New("w9d zzq version archive refused")
	cases := []struct {
		name  string
		setup func(r *w9dArchiveRig, tenantID, wfID uuid.UUID)
		want  error
	}{
		{
			name: "the workflow is unknown",
			setup: func(r *w9dArchiveRig, tenantID, wfID uuid.UUID) {
				r.wf.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(nil, domain.ErrNotFound)
			},
			want: domain.ErrNotFound,
		},
		{
			name: "the workflow has no active version",
			setup: func(r *w9dArchiveRig, tenantID, wfID uuid.UUID) {
				r.wf.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{}, nil)
			},
			want: domain.ErrNoActiveVersion,
		},
		{
			name: "instances are still running",
			setup: func(r *w9dArchiveRig, tenantID, wfID uuid.UUID) {
				r.wf.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(withActive, nil)
				r.exec.EXPECT().CheckActiveInstances(gomock.Any(), tenantID, wfID).Return(true, int32(2), nil)
			},
			want: domain.ErrActiveInstancesExist,
		},
		{
			name: "the active-instance check fails",
			setup: func(r *w9dArchiveRig, tenantID, wfID uuid.UUID) {
				r.wf.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(withActive, nil)
				r.exec.EXPECT().CheckActiveInstances(gomock.Any(), tenantID, wfID).Return(false, int32(0), domain.ErrUpstreamUnavailable)
			},
			want: domain.ErrUpstreamUnavailable,
		},
		{
			name: "the version archive write fails",
			setup: func(r *w9dArchiveRig, tenantID, wfID uuid.UUID) {
				r.wf.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(withActive, nil).Times(2)
				r.exec.EXPECT().CheckActiveInstances(gomock.Any(), tenantID, wfID).Return(false, int32(0), nil)
				r.runTxInline()
				r.vers.EXPECT().Archive(gomock.Any(), tenantID, activeID).Return(txFailure)
			},
			want: txFailure,
		},
		{
			name: "the workflow update fails after the version was archived",
			setup: func(r *w9dArchiveRig, tenantID, wfID uuid.UUID) {
				r.wf.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(withActive, nil).Times(2)
				r.exec.EXPECT().CheckActiveInstances(gomock.Any(), tenantID, wfID).Return(false, int32(0), nil)
				r.runTxInline()
				r.vers.EXPECT().Archive(gomock.Any(), tenantID, activeID).Return(nil)
				r.wf.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, (*uuid.UUID)(nil)).Return(txFailure)
			},
			want: txFailure,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			r := newW9dArchiveRig(ctrl)
			tenantID, wfID := uuid.New(), uuid.New()
			tc.setup(r, tenantID, wfID)

			err := r.svc.Archive(context.Background(), tenantID, uuid.New(), wfID)

			require.ErrorIs(t, err, tc.want)
			assert.Empty(t, r.log.warnLines())
			for _, line := range r.log.infoLines() {
				assert.NotEqual(t, "workflow archived", line.msg, "a failed archive is not logged as done")
			}
		})
	}
}

func TestWorkflowService_Archive_CacheEvictionFails_ArchiveStillSucceedsAndOneWarningNamesTheVersion(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	r := newW9dArchiveRig(ctrl)
	tenantID, userID, wfID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	r.wf.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{ID: wfID, ActiveVersionID: &activeID}, nil).Times(2)
	r.exec.EXPECT().CheckActiveInstances(gomock.Any(), tenantID, wfID).Return(false, int32(0), nil)
	r.runTxInline()
	r.vers.EXPECT().Archive(gomock.Any(), tenantID, activeID).Return(nil)
	r.wf.EXPECT().UpdateActiveVersion(gomock.Any(), tenantID, wfID, (*uuid.UUID)(nil)).Return(nil)
	r.cache.EXPECT().Del(gomock.Any(), domain.CompiledPlanCacheKey(tenantID, activeID)).
		Return(errors.New("w9d zzq valkey unreachable"))

	err := r.svc.Archive(context.Background(), tenantID, userID, wfID)

	require.NoError(t, err)
	warns := r.log.warnLines()
	require.Len(t, warns, 1)
	assert.Equal(t, "compiled-plan cache invalidation failed", warns[0].msg)
	assert.Equal(t, activeID.String(), warns[0].fields["version_id"])
	errText, ok := warns[0].fields["error"].(string)
	require.True(t, ok, "the warning carries the error text")
	assert.Contains(t, errText, "w9d zzq valkey unreachable")
	assert.Contains(t, errText, activeID.String(), "the error names the version whose entry stayed")
	var archived []w9dLogLine
	for _, line := range r.log.infoLines() {
		if line.msg == "workflow archived" {
			archived = append(archived, line)
		}
	}
	require.Len(t, archived, 1, "the archive is still reported as done")
	assert.Equal(t, activeID.String(), archived[0].fields["version_id"])
}

func TestDraftService_Init_DraftCarriesOnlyTheBPMNOfTheActiveVersion(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, wfRepo, vRepo)
	tenantID, userID, wfID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	publishedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	number, plan, invalid := int32(7), `{"w9d":"plan"}`, `[{"code":"W9D"}]`
	active := &domain.WorkflowVersion{
		ID: activeID, WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusPublished,
		BPMNXML: "<w9d-active/>", CompiledPlanJSON: &plan, ArtifactHash: "w9d-hash", VersionNumber: &number,
		PublishedAt: &publishedAt, CreatedByUserID: uuid.New(), IsValid: false, ValidationErrorsJSON: &invalid,
		CreatedAt: publishedAt, UpdatedAt: publishedAt, RecordVersion: 9,
		ModuleRefs: []domain.ModuleRef{{ModuleID: uuid.New(), VersionID: uuid.New(), ProcessID: "w9d_module", VersionNumber: 2}},
	}
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ID: wfID, ActiveVersionID: &activeID}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeID).Return(active, nil)
	var stored []*domain.WorkflowVersion
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, v *domain.WorkflowVersion) error {
			stored = append(stored, v)
			return nil
		})

	returned, err := svc.Init(context.Background(), tenantID, userID, wfID)

	require.NoError(t, err)
	require.Len(t, stored, 1)
	draft := stored[0]
	assert.Same(t, draft, returned)
	assert.Equal(t, "<w9d-active/>", draft.BPMNXML)
	assert.Equal(t, domain.VersionStatusDraft, draft.Status)
	assert.Equal(t, wfID, draft.WorkflowID)
	assert.Equal(t, tenantID, draft.TenantID)
	assert.Equal(t, userID, draft.CreatedByUserID, "the draft is credited to the caller, not to the author of the active version")
	assert.True(t, draft.IsValid, "a fresh draft starts valid even though the active version was marked invalid")
	assert.NotEqual(t, activeID, draft.ID)
	w9dRequireUUIDv7(t, draft.ID)
	assert.Nil(t, draft.CompiledPlanJSON)
	assert.Empty(t, draft.ArtifactHash)
	assert.Nil(t, draft.VersionNumber)
	assert.Nil(t, draft.PublishedAt)
	assert.Nil(t, draft.ValidationErrorsJSON)
	assert.Zero(t, draft.RecordVersion)
	assert.Empty(t, draft.ModuleRefs)
	assert.Equal(t, "<w9d-active/>", active.BPMNXML, "the active version is left as it was")
	assert.Equal(t, domain.VersionStatusPublished, active.Status)
}

var _ port.Logger = (*w9dLog)(nil)
