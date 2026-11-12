package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	bpmncompiler "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type w5aTxKey struct{}

type w5aDraftMocks struct {
	cache *mocks.MockCacheStore
	wf    *mocks.MockWorkflowRepository
	vers  *mocks.MockWorkflowVersionRepository
	tx    *mocks.MockTransactor
}

func w5aNewDraftService(ctrl *gomock.Controller, withCache, withTx bool) (*service.DraftService, w5aDraftMocks) {
	m := w5aDraftMocks{
		cache: mocks.NewMockCacheStore(ctrl),
		wf:    mocks.NewMockWorkflowRepository(ctrl),
		vers:  mocks.NewMockWorkflowVersionRepository(ctrl),
		tx:    mocks.NewMockTransactor(ctrl),
	}
	deps := service.DraftDeps{
		Workflows: m.wf,
		Versions:  m.vers,
		Assignees: mocks.NewMockAssigneeRepository(ctrl),
		Compiler:  bpmncompiler.New(),
	}
	if withCache {
		deps.Cache = m.cache
	}
	if withTx {
		deps.Transactor = m.tx
	}
	return service.NewDraftService(deps), m
}

func TestDraftService_Update_TakesTheTenantWorkflowLockAt30sAndReleasesItWithItsTokenAfterTheRequestIsCancelled(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := w5aNewDraftService(ctrl, true, false)
	tenantID, userID, wfID := uuid.New(), uuid.New(), uuid.New()
	wantKey := "draft-lock:" + tenantID.String() + ":" + wfID.String()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var tookKey, tookToken string
	var tookTTL time.Duration
	m.cache.EXPECT().SetNX(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, key, token string, ttl time.Duration) (bool, error) {
			tookKey, tookToken, tookTTL = key, token, ttl
			return true, nil
		})
	var releasedKey, releasedToken string
	var releaseCtxErr error
	releases := 0
	m.cache.EXPECT().DelIfEqual(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(rctx context.Context, key, token string) error {
			releases++
			releasedKey, releasedToken, releaseCtxErr = key, token, rctx.Err()
			return nil
		})
	m.vers.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(&domain.WorkflowVersion{ID: uuid.New(), RecordVersion: 1}, nil)
	m.vers.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, *domain.WorkflowVersion) error {
			cancel()
			return nil
		})

	_, err := svc.Update(ctx, tenantID, userID, wfID, service.UpdateDraftReq{RecordVersion: 1})

	require.NoError(t, err)
	assert.Equal(t, wantKey, tookKey)
	assert.Equal(t, 30*time.Second, tookTTL)
	assert.NotEmpty(t, tookToken)
	assert.Equal(t, 1, releases)
	assert.Equal(t, wantKey, releasedKey)
	assert.Equal(t, tookToken, releasedToken)
	require.Error(t, ctx.Err(), "the request context was cancelled before the release")
	assert.NoError(t, releaseCtxErr, "the release runs on a context that outlives the cancelled request")
}

func TestDraftService_Update_WithoutACache_TakesNoLockAndSavesWithTheRecordVersionItRead(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc, m := w5aNewDraftService(ctrl, false, false)
	tenantID, wfID := uuid.New(), uuid.New()
	read := &domain.WorkflowVersion{
		ID: uuid.New(), WorkflowID: wfID, TenantID: tenantID, Status: domain.VersionStatusDraft,
		BPMNXML: "<bpmn/>", IsValid: false, RecordVersion: 7,
	}
	readBefore := *read
	m.vers.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(read, nil)
	var saved *domain.WorkflowVersion
	m.vers.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, v *domain.WorkflowVersion) error {
			copied := *v
			saved = &copied
			return nil
		})

	_, err := svc.Update(context.Background(), tenantID, uuid.New(), wfID, service.UpdateDraftReq{RecordVersion: 7})

	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, readBefore, *saved, "the draft that was read is the one saved, with every other field unchanged")
}

func TestDraftService_Update_RunsMetadataAndDraftWritesInOneTransactionOnlyWhenMetadataChanges(t *testing.T) {
	name, description, xml := "new-name", "new-desc", "<bpmn2/>"
	cases := []struct {
		name         string
		req          service.UpdateDraftReq
		wantTxRuns   int
		wantMetadata bool
		wantName     string
		wantDesc     string
	}{
		{name: "name only", req: service.UpdateDraftReq{Name: &name, RecordVersion: 1}, wantTxRuns: 1, wantMetadata: true, wantName: name, wantDesc: "old-desc"},
		{name: "description only", req: service.UpdateDraftReq{Description: &description, RecordVersion: 1}, wantTxRuns: 1, wantMetadata: true, wantName: "old", wantDesc: description},
		{name: "name and xml", req: service.UpdateDraftReq{Name: &name, BPMNXML: &xml, RecordVersion: 1}, wantTxRuns: 1, wantMetadata: true, wantName: name, wantDesc: "old-desc"},
		{name: "xml only", req: service.UpdateDraftReq{BPMNXML: &xml, RecordVersion: 1}, wantTxRuns: 0, wantMetadata: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			svc, m := w5aNewDraftService(ctrl, false, true)
			tenantID, wfID := uuid.New(), uuid.New()
			txRuns := 0
			m.tx.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, fn func(context.Context) error) error {
					txRuns++
					return fn(context.WithValue(ctx, w5aTxKey{}, true))
				}).Times(tc.wantTxRuns)
			m.vers.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(&domain.WorkflowVersion{ID: uuid.New(), RecordVersion: 1}, nil)
			metadataInTx, draftInTx := false, false
			var gotName, gotDesc string
			if tc.wantMetadata {
				m.wf.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{Name: "old", Description: "old-desc"}, nil)
				m.wf.EXPECT().UpdateMetadata(gomock.Any(), tenantID, wfID, gomock.Any(), gomock.Any()).DoAndReturn(
					func(ctx context.Context, _, _ uuid.UUID, name, description string) error {
						gotName, gotDesc = name, description
						metadataInTx = ctx.Value(w5aTxKey{}) == true
						return nil
					})
			}
			m.vers.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, _ *domain.WorkflowVersion) error {
					draftInTx = ctx.Value(w5aTxKey{}) == true
					return nil
				})

			_, err := svc.Update(context.Background(), tenantID, uuid.New(), wfID, tc.req)

			require.NoError(t, err)
			assert.Equal(t, tc.wantTxRuns, txRuns)
			assert.Equal(t, tc.wantMetadata, metadataInTx, "the metadata write runs inside the transaction")
			assert.Equal(t, tc.wantName, gotName, "a field the request leaves out keeps its stored value")
			assert.Equal(t, tc.wantDesc, gotDesc, "a field the request leaves out keeps its stored value")
			assert.Equal(t, tc.wantTxRuns == 1, draftInTx, "the draft write shares the metadata's transaction, and runs outside one when there is no metadata")
		})
	}
}

func TestDraftService_Update_ADraftWriteFailureFailsTheTransactionThatHoldsTheMetadataWrite(t *testing.T) {
	ctrl := gomock.NewController(t)
	tx := &w5bTx{}
	wf := mocks.NewMockWorkflowRepository(ctrl)
	vers := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := service.NewDraftService(service.DraftDeps{
		Transactor: tx,
		Workflows:  wf,
		Versions:   vers,
		Assignees:  mocks.NewMockAssigneeRepository(ctrl),
		Compiler:   bpmncompiler.New(),
	})
	tenantID, wfID := uuid.New(), uuid.New()
	name := "new-name"
	boom := errors.New("draft write failed")
	var order []string
	vers.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(&domain.WorkflowVersion{ID: uuid.New(), RecordVersion: 1}, nil)
	wf.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{Name: "old", Description: "d"}, nil)
	wf.EXPECT().UpdateMetadata(gomock.Any(), tenantID, wfID, name, "d").DoAndReturn(
		func(ctx context.Context, _, _ uuid.UUID, _, _ string) error {
			order = append(order, "metadata")
			tx.write(ctx, "workflow metadata")
			return nil
		})
	vers.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, *domain.WorkflowVersion) error {
			order = append(order, "draft")
			return boom
		})

	got, err := svc.Update(context.Background(), tenantID, uuid.New(), wfID, service.UpdateDraftReq{Name: &name, RecordVersion: 1})

	assert.Nil(t, got)
	require.ErrorIs(t, err, boom)
	assert.Equal(t, []string{"metadata", "draft"}, order)
	assert.Equal(t, 1, tx.started)
	assert.Equal(t, 1, tx.rollbacks, "the failure leaves the transaction callback as an error, which is what makes the transactor roll back")
	assert.Zero(t, tx.commits)
	assert.Empty(t, tx.persisted(), "the metadata write is rolled back with the failed draft write")
}
