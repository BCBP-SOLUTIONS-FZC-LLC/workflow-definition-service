package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestDraftService_Init_StoresAValidPlanlessDraftOwnedByTheCaller(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, wfRepo, vRepo)

	tenantID, userID, wfID, activeVID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	plan := `{"plan":"of the active version"}`
	activeNumber := int32(7)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{ActiveVersionID: &activeVID}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeVID).Return(&domain.WorkflowVersion{
		ID: activeVID, BPMNXML: "<active/>", CompiledPlanJSON: &plan, ArtifactHash: "active-hash",
		VersionNumber: &activeNumber, Status: domain.VersionStatusPublished, IsValid: false,
	}, nil)
	var stored *domain.WorkflowVersion
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, v *domain.WorkflowVersion) error {
			stored = v
			return nil
		})

	draft, err := svc.Init(context.Background(), tenantID, userID, wfID)

	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Same(t, stored, draft, "the draft handed back is the one stored")
	assert.NotEqual(t, uuid.Nil, draft.ID)
	assert.NotEqual(t, activeVID, draft.ID)
	assert.Equal(t, tenantID, draft.TenantID)
	assert.Equal(t, wfID, draft.WorkflowID)
	assert.Equal(t, userID, draft.CreatedByUserID)
	assert.Equal(t, domain.VersionStatusDraft, draft.Status)
	assert.Equal(t, "<active/>", draft.BPMNXML)
	assert.True(t, draft.IsValid)
	assert.Nil(t, draft.CompiledPlanJSON, "the draft does not inherit the active version's plan")
	assert.Empty(t, draft.ArtifactHash, "the draft does not inherit the active version's hash")
	assert.Nil(t, draft.VersionNumber, "the draft does not inherit the active version's number")
}

func TestDraftService_Init_ACreateFailureIsReturnedWrapped(t *testing.T) {
	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, wfRepo, vRepo)

	tenantID, wfID, activeVID := uuid.New(), uuid.New(), uuid.New()
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{ActiveVersionID: &activeVID}, nil)
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, activeVID).
		Return(&domain.WorkflowVersion{ID: activeVID, BPMNXML: "<active/>"}, nil)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(domain.ErrDraftAlreadyExists)

	got, err := svc.Init(context.Background(), tenantID, uuid.New(), wfID)

	assert.Nil(t, got)
	require.ErrorIs(t, err, domain.ErrDraftAlreadyExists)
	assert.NotEqual(t, domain.ErrDraftAlreadyExists, err, "the repository error is wrapped, not passed through bare")
	assert.Contains(t, err.Error(), "create draft: ")
}

func TestDraftService_Update_BPMNReplacesTheXMLAndResetsWhatWasCompiledFromIt(t *testing.T) {
	plan := `{"plan":"compiled from the old xml"}`
	newXML := "<new/>"
	cases := []struct {
		name      string
		bpmn      *string
		wantXML   string
		wantPlan  *string
		wantHash  string
		wantValid bool
	}{
		{name: "bpmn set", bpmn: &newXML, wantXML: "<new/>", wantPlan: nil, wantHash: "", wantValid: true},
		{name: "bpmn absent", bpmn: nil, wantXML: "<old/>", wantPlan: &plan, wantHash: "old-hash", wantValid: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			cache := mocks.NewMockCacheStore(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			svc := newDraftSvc(ctrl, cache, mocks.NewMockWorkflowRepository(ctrl), vRepo)

			tenantID, userID, wfID := uuid.New(), uuid.New(), uuid.New()
			draft := &domain.WorkflowVersion{
				ID: uuid.New(), BPMNXML: "<old/>", CompiledPlanJSON: &plan, ArtifactHash: "old-hash",
				IsValid: false, RecordVersion: 4, UpdatedAt: time.Now(),
			}
			lockKey := "draft-lock:" + tenantID.String() + ":" + wfID.String()
			var token string
			cache.EXPECT().SetNX(gomock.Any(), lockKey, gomock.Any(), 30*time.Second).DoAndReturn(
				func(_ context.Context, _ string, value string, _ time.Duration) (bool, error) {
					token = value
					return true, nil
				})
			cache.EXPECT().DelIfEqual(gomock.Any(), lockKey, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, value string) error {
					assert.NotEmpty(t, token)
					assert.Equal(t, token, value, "the lock is released with the token it was taken with")
					return nil
				}).Times(1)
			vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(draft, nil)
			var saved *domain.WorkflowVersion
			vRepo.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, v *domain.WorkflowVersion) error {
					copied := *v
					saved = &copied
					return nil
				})

			got, err := svc.Update(context.Background(), tenantID, userID, wfID, service.UpdateDraftReq{
				BPMNXML: tc.bpmn, RecordVersion: 4,
			})

			require.NoError(t, err)
			require.NotNil(t, saved)
			for _, v := range []*domain.WorkflowVersion{saved, got} {
				assert.Equal(t, tc.wantXML, v.BPMNXML)
				assert.Equal(t, tc.wantPlan, v.CompiledPlanJSON)
				assert.Equal(t, tc.wantHash, v.ArtifactHash)
				assert.Equal(t, tc.wantValid, v.IsValid)
			}
		})
	}
}

func TestDraftService_Get_WithNoDraft_WrapsTheSentinelUnderGetDraftForTheCallersWorkflow(t *testing.T) {
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	svc := newDraftSvc(ctrl, nil, nil, vRepo)

	tenantID, wfID := uuid.New(), uuid.New()
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(nil, domain.ErrNoDraftExists)

	got, err := svc.Get(context.Background(), tenantID, wfID)

	assert.Nil(t, got)
	require.ErrorIs(t, err, domain.ErrNoDraftExists)
	assert.Contains(t, err.Error(), "get draft: ")
}
