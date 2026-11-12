package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type archiveTxKey struct{}

type insideArchiveTx struct{}

func (insideArchiveTx) Matches(x any) bool {
	ctx, ok := x.(context.Context)
	return ok && ctx.Value(archiveTxKey{}) == true
}

func (insideArchiveTx) String() string { return "a context carrying the archive transaction" }

func markedTransaction(m moduleMocks, global bool) {
	tx := m.tx
	if global {
		tx = m.globalTx
	}
	run := func(ctx context.Context, f func(context.Context) error) error {
		return f(context.WithValue(ctx, archiveTxKey{}, true))
	}
	tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(run)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(run)
}

func int32p(n int32) *int32 { return &n }

const archiveModuleProcessID = "Process_Library"

type archiveFixture struct {
	tenantID, moduleID, activeID uuid.UUID
	module                       *domain.Module
}

func newArchiveFixture(scope domain.CatalogScope) archiveFixture {
	f := archiveFixture{tenantID: uuid.New(), moduleID: uuid.New(), activeID: uuid.New()}
	f.module = &domain.Module{ID: f.moduleID, Scope: scope, ProcessID: archiveModuleProcessID, ActiveVersionID: &f.activeID}
	return f
}

func (f archiveFixture) expectModuleReads(m moduleMocks) {
	if f.module.Scope == domain.ScopeGlobal {
		m.modules.EXPECT().GetByID(gomock.Any(), f.tenantID, f.moduleID).Return(f.module, nil)
		m.globalModules.EXPECT().GetByID(insideArchiveTx{}, f.tenantID, f.moduleID).Return(f.module, nil)
		return
	}
	m.modules.EXPECT().GetByID(gomock.Any(), f.tenantID, f.moduleID).Return(f.module, nil).Times(2)
}

func (f archiveFixture) expectArchived(m moduleMocks) {
	versions, modules := m.versions, m.modules
	if f.module.Scope == domain.ScopeGlobal {
		versions, modules = m.globalVersions, m.globalModules
	}
	versions.EXPECT().Archive(insideArchiveTx{}, f.tenantID, f.activeID).Return(nil)
	modules.EXPECT().UpdateActiveVersion(insideArchiveTx{}, f.moduleID, (*uuid.UUID)(nil)).Return(nil)
}

func TestModuleService_Archive_RefusedWhileInUse(t *testing.T) {
	wfPublished, wfDraft, wfBoth, claimsV7 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	draftXML, otherXML := "<draft-calls-module/>", "<draft-calls-other/>"
	cases := []struct {
		name     string
		usages   []*domain.ModuleUsage
		drafts   []*domain.DraftSource
		calls    map[string][]domain.ModuleCall
		wantUses []domain.ModuleUse
	}{
		{
			name: "a published workflow version holds the module in its module_refs",
			usages: []*domain.ModuleUsage{
				{WorkflowID: wfPublished, WorkflowKey: "procurement", VersionID: uuid.New(), VersionNumber: int32p(4),
					Status: domain.VersionStatusPublished, ModuleVersionNumber: 2},
			},
			wantUses: []domain.ModuleUse{
				{WorkflowID: wfPublished, WorkflowKey: "procurement", Status: domain.VersionStatusPublished, VersionNumber: int32p(4)},
			},
		},
		{
			name: "a workflow draft has a call activity naming the module's process id",
			drafts: []*domain.DraftSource{
				{WorkflowID: wfDraft, WorkflowKey: "onboarding", BPMNXML: draftXML},
				{WorkflowID: uuid.New(), WorkflowKey: "other", BPMNXML: otherXML},
			},
			calls: map[string][]domain.ModuleCall{
				draftXML: {{CallID: "call_1", ProcessID: archiveModuleProcessID}},
				otherXML: {{CallID: "call_2", ProcessID: "Process_Unrelated"}},
			},
			wantUses: []domain.ModuleUse{
				{WorkflowID: wfDraft, WorkflowKey: "onboarding", Status: domain.VersionStatusDraft},
			},
		},
		{
			name: "a draft that pins a version of the module counts too",
			drafts: []*domain.DraftSource{
				{WorkflowID: wfDraft, WorkflowKey: "onboarding", BPMNXML: draftXML},
			},
			calls: map[string][]domain.ModuleCall{
				draftXML: {{CallID: "call_1", ProcessID: archiveModuleProcessID, VersionNumber: 3}},
			},
			wantUses: []domain.ModuleUse{
				{WorkflowID: wfDraft, WorkflowKey: "onboarding", Status: domain.VersionStatusDraft},
			},
		},
		{
			name: "published versions and drafts are all listed, a version holding two module versions once, archived ones not at all",
			usages: []*domain.ModuleUsage{
				{WorkflowID: wfBoth, WorkflowKey: "claims", VersionID: claimsV7, VersionNumber: int32p(7),
					Status: domain.VersionStatusPublished, ModuleVersionNumber: 1},
				{WorkflowID: wfBoth, WorkflowKey: "claims", VersionID: claimsV7, VersionNumber: int32p(7),
					Status: domain.VersionStatusPublished, ModuleVersionNumber: 2},
				{WorkflowID: wfBoth, WorkflowKey: "claims", VersionID: uuid.New(), VersionNumber: int32p(6),
					Status: domain.VersionStatusArchived, ModuleVersionNumber: 1},
			},
			drafts: []*domain.DraftSource{{WorkflowID: wfBoth, WorkflowKey: "claims", BPMNXML: draftXML}},
			calls: map[string][]domain.ModuleCall{
				draftXML: {{CallID: "call_1", ProcessID: archiveModuleProcessID}, {CallID: "call_2", ProcessID: archiveModuleProcessID}},
			},
			wantUses: []domain.ModuleUse{
				{WorkflowID: wfBoth, WorkflowKey: "claims", Status: domain.VersionStatusPublished, VersionNumber: int32p(7)},
				{WorkflowID: wfBoth, WorkflowKey: "claims", Status: domain.VersionStatusDraft},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			svc, m := newModuleService(ctrl)
			markedTransaction(m, false)
			f := newArchiveFixture(domain.ScopeTenant)
			f.expectModuleReads(m)
			m.workflowVersions.EXPECT().ListModuleUsages(insideArchiveTx{}, f.tenantID, f.moduleID).Return(tc.usages, nil)
			m.workflowVersions.EXPECT().ListDraftSources(insideArchiveTx{}, f.tenantID).Return(tc.drafts, nil)
			for xml, calls := range tc.calls {
				m.compiler.EXPECT().ModuleCalls(insideArchiveTx{}, xml).Return(calls, nil, nil)
			}

			err := svc.Archive(context.Background(), f.tenantID, uuid.New(), f.moduleID)

			require.ErrorIs(t, err, domain.ErrModuleInUse)
			var inUse *domain.ModuleInUseError
			require.ErrorAs(t, err, &inUse)
			assert.Equal(t, tc.wantUses, inUse.Uses)
		})
	}
}

func TestModuleService_Archive_NotInUse_Archives(t *testing.T) {
	wfOld, wfDraft := uuid.New(), uuid.New()
	draftXML := "<draft-calls-other/>"
	cases := []struct {
		name   string
		usages []*domain.ModuleUsage
		drafts []*domain.DraftSource
		calls  map[string][]domain.ModuleCall
	}{
		{name: "nothing uses the module"},
		{
			name: "only archived workflow versions hold the module",
			usages: []*domain.ModuleUsage{
				{WorkflowID: wfOld, WorkflowKey: "retired", VersionID: uuid.New(), VersionNumber: int32p(2),
					Status: domain.VersionStatusArchived, ModuleVersionNumber: 1},
				{WorkflowID: wfOld, WorkflowKey: "retired", VersionID: uuid.New(), VersionNumber: int32p(1),
					Status: domain.VersionStatusArchived, ModuleVersionNumber: 1},
			},
		},
		{
			name:   "drafts exist but none calls this module",
			drafts: []*domain.DraftSource{{WorkflowID: wfDraft, WorkflowKey: "other", BPMNXML: draftXML}},
			calls:  map[string][]domain.ModuleCall{draftXML: {{CallID: "call_1", ProcessID: "Process_Unrelated"}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			svc, m := newModuleService(ctrl)
			markedTransaction(m, false)
			f := newArchiveFixture(domain.ScopeTenant)
			f.expectModuleReads(m)
			m.workflowVersions.EXPECT().ListModuleUsages(insideArchiveTx{}, f.tenantID, f.moduleID).Return(tc.usages, nil)
			m.workflowVersions.EXPECT().ListDraftSources(insideArchiveTx{}, f.tenantID).Return(tc.drafts, nil)
			for xml, calls := range tc.calls {
				m.compiler.EXPECT().ModuleCalls(insideArchiveTx{}, xml).Return(calls, nil, nil)
			}
			f.expectArchived(m)

			err := svc.Archive(context.Background(), f.tenantID, uuid.New(), f.moduleID)

			require.NoError(t, err)
		})
	}
}

func TestModuleService_Archive_UsageCheckRunsInsideTheArchiveTransaction(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := newModuleService(ctrl)
	markedTransaction(m, false)
	f := newArchiveFixture(domain.ScopeTenant)
	draftXML := "<draft/>"
	m.modules.EXPECT().GetByID(gomock.Any(), f.tenantID, f.moduleID).Return(f.module, nil).Times(2)
	gomock.InOrder(
		m.workflowVersions.EXPECT().ListModuleUsages(insideArchiveTx{}, f.tenantID, f.moduleID).Return(nil, nil),
		m.workflowVersions.EXPECT().ListDraftSources(insideArchiveTx{}, f.tenantID).
			Return([]*domain.DraftSource{{WorkflowID: uuid.New(), WorkflowKey: "k", BPMNXML: draftXML}}, nil),
		m.compiler.EXPECT().ModuleCalls(insideArchiveTx{}, draftXML).Return(nil, nil, nil),
		m.versions.EXPECT().Archive(insideArchiveTx{}, f.tenantID, f.activeID).Return(nil),
		m.modules.EXPECT().UpdateActiveVersion(insideArchiveTx{}, f.moduleID, (*uuid.UUID)(nil)).Return(nil),
	)

	require.NoError(t, svc.Archive(context.Background(), f.tenantID, uuid.New(), f.moduleID))
}

func TestModuleService_Archive_GlobalModule_ChecksOnlyTheCallersTenantInsideTheSystemTransaction(t *testing.T) {
	published := &domain.ModuleUsage{WorkflowID: uuid.New(), WorkflowKey: "uses-global", VersionID: uuid.New(),
		VersionNumber: int32p(1), Status: domain.VersionStatusPublished, ModuleVersionNumber: 1}
	cases := []struct {
		name    string
		usages  []*domain.ModuleUsage
		wantUse bool
	}{
		{name: "the caller's tenant has a published version that uses it", usages: []*domain.ModuleUsage{published}, wantUse: true},
		{name: "the caller's tenant does not use it"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			svc, m := newModuleService(ctrl)
			markedTransaction(m, true)
			f := newArchiveFixture(domain.ScopeGlobal)
			f.expectModuleReads(m)
			m.workflowVersions.EXPECT().ListModuleUsages(insideArchiveTx{}, f.tenantID, f.moduleID).Return(tc.usages, nil)
			m.workflowVersions.EXPECT().ListDraftSources(insideArchiveTx{}, f.tenantID).Return(nil, nil)
			if !tc.wantUse {
				f.expectArchived(m)
			}

			err := svc.Archive(context.Background(), f.tenantID, uuid.New(), f.moduleID)

			if tc.wantUse {
				require.ErrorIs(t, err, domain.ErrModuleInUse)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestModuleService_Archive_UsageCheckFailure_ArchivesNothing(t *testing.T) {
	outage := fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable)
	draftXML := "<draft/>"
	cases := []struct {
		name    string
		setup   func(m moduleMocks, f archiveFixture)
		wantErr error
	}{
		{
			name: "the usages lookup is unavailable",
			setup: func(m moduleMocks, f archiveFixture) {
				m.workflowVersions.EXPECT().ListModuleUsages(insideArchiveTx{}, f.tenantID, f.moduleID).Return(nil, outage)
			},
			wantErr: domain.ErrUpstreamUnavailable,
		},
		{
			name: "the draft lookup is unavailable",
			setup: func(m moduleMocks, f archiveFixture) {
				m.workflowVersions.EXPECT().ListModuleUsages(insideArchiveTx{}, f.tenantID, f.moduleID).Return(nil, nil)
				m.workflowVersions.EXPECT().ListDraftSources(insideArchiveTx{}, f.tenantID).Return(nil, outage)
			},
			wantErr: domain.ErrUpstreamUnavailable,
		},
		{
			name: "reading a draft's calls is cancelled",
			setup: func(m moduleMocks, f archiveFixture) {
				m.workflowVersions.EXPECT().ListModuleUsages(insideArchiveTx{}, f.tenantID, f.moduleID).Return(nil, nil)
				m.workflowVersions.EXPECT().ListDraftSources(insideArchiveTx{}, f.tenantID).
					Return([]*domain.DraftSource{{WorkflowID: uuid.New(), WorkflowKey: "k", BPMNXML: draftXML}}, nil)
				m.compiler.EXPECT().ModuleCalls(insideArchiveTx{}, draftXML).Return(nil, nil, context.Canceled)
			},
			wantErr: context.Canceled,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			svc, m := newModuleService(ctrl)
			markedTransaction(m, false)
			f := newArchiveFixture(domain.ScopeTenant)
			f.expectModuleReads(m)
			tc.setup(m, f)

			err := svc.Archive(context.Background(), f.tenantID, uuid.New(), f.moduleID)

			require.Error(t, err)
			assert.True(t, errors.Is(err, tc.wantErr), "Archive() = %v, want %v", err, tc.wantErr)
			assert.False(t, errors.Is(err, domain.ErrModuleInUse))
		})
	}
}
