package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestModuleService_Publish_AStepOfTheTransactionFails_RollsBackAndTheDraftStaysDraft(t *testing.T) {
	t.Parallel()

	nextErr := errors.New("sequence read failed")
	updateErr := errors.New("module pointer write failed")
	cases := []struct {
		name       string
		nextErr    error
		publishErr error
		updateErr  error
		wantCause  error
		wantPrefix string
		wrote      bool
	}{
		{name: "the next version number cannot be read", nextErr: nextErr, wantCause: nextErr, wantPrefix: "publish module version: next module version number: "},
		{name: "the version row cannot be published and keeps its own type", publishErr: domain.ErrVersionNotDraft, wantCause: domain.ErrVersionNotDraft, wantPrefix: "publish module version: "},
		{name: "the module's active version cannot be moved after the version was published", updateErr: updateErr, wantCause: updateErr, wantPrefix: "publish module version: ", wrote: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			tx := &w5bTx{}
			modules := mocks.NewMockModuleRepository(ctrl)
			versions := mocks.NewMockModuleVersionRepository(ctrl)
			compiler := mocks.NewMockPlanCompiler(ctrl)
			svc := service.NewModuleService(service.ModuleDeps{
				Transactor: tx, Modules: modules, Versions: versions, Compiler: compiler,
			})
			tenantID, userID, moduleID, versionID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			draft := &domain.ModuleVersion{
				ID: versionID, ModuleID: moduleID, Scope: domain.ScopeTenant, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
			}

			versions.EXPECT().GetByID(gomock.Any(), tenantID, versionID).Return(draft, nil)
			compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
			compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
			compiler.EXPECT().ValidateModule(gomock.Any(), "<bpmn/>", "", false).Return(nil, nil)

			if tc.nextErr != nil {
				versions.EXPECT().NextVersionNumber(gomock.Any(), moduleID).Return(int32(0), tc.nextErr)
			} else {
				versions.EXPECT().NextVersionNumber(gomock.Any(), moduleID).Return(int32(4), nil)
				versions.EXPECT().Publish(gomock.Any(), tenantID, versionID, int32(4)).
					DoAndReturn(func(ctx context.Context, _, _ uuid.UUID, _ int32) error {
						if tc.publishErr != nil {
							return tc.publishErr
						}
						tx.write(ctx, "version published")
						return nil
					})
			}
			if tc.publishErr == nil && tc.nextErr == nil {
				modules.EXPECT().UpdateActiveVersion(gomock.Any(), moduleID, &versionID).
					Return(tc.updateErr)
			}

			got, err := svc.Publish(context.Background(), tenantID, userID, moduleID, versionID)

			require.Error(t, err)
			assert.Nil(t, got)
			assert.ErrorIs(t, err, tc.wantCause)
			assert.True(t, strings.HasPrefix(err.Error(), tc.wantPrefix), "err = %q", err)
			assert.Equal(t, 1, tx.retryStart, "the three steps run in one retried transaction")
			assert.Equal(t, 1, tx.rollbacks, "the failing step leaves the transaction through its error")
			assert.Zero(t, tx.commits)
			if tc.wrote {
				assert.Empty(t, tx.persisted(), "nothing the transaction wrote survives")
			}
			assert.Equal(t, domain.VersionStatusDraft, draft.Status)
			assert.Nil(t, draft.VersionNumber)
		})
	}
}
