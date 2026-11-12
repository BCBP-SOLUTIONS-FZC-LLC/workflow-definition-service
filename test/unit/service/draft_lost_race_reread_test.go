package service_test

import (
	"context"
	"errors"
	"sync"
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
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

type draftRaceLogEntry struct {
	msg    string
	fields map[string]any
}

type draftRaceLog struct {
	testkit.FakeLogger
	mu    sync.Mutex
	warns []draftRaceLogEntry
}

func (l *draftRaceLog) Warn(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, draftRaceLogEntry{msg: msg, fields: fields})
}

func TestDraftService_Update_LostSaveRace_RereadOutcomeDecidesTheAnswer(t *testing.T) {
	storeDown := errors.Join(domain.ErrUpstreamUnavailable, errors.New("connection refused"))
	cases := []struct {
		name         string
		rereadErr    error
		workflowErr  error
		wantIs       error
		wantWarn     bool
		wantConflict bool
	}{
		{name: "draft deleted, workflow exists", rereadErr: domain.ErrNotFound, wantIs: domain.ErrNoDraftExists, wantWarn: true},
		{name: "draft deleted, workflow gone too", rereadErr: domain.ErrNotFound, workflowErr: domain.ErrNotFound, wantIs: domain.ErrNotFound, wantWarn: true},
		{name: "database fault on the re-read", rereadErr: storeDown, wantIs: domain.ErrUpstreamUnavailable, wantWarn: true},
		{name: "re-read succeeds", wantConflict: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID, userID, wfID := uuid.New(), uuid.New(), uuid.New()
			ctrl := gomock.NewController(t)
			cache := mocks.NewMockCacheStore(ctrl)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			log := &draftRaceLog{}
			svc := service.NewDraftService(service.DraftDeps{
				Cache: cache, Workflows: wfRepo, Versions: vRepo, Log: log, Compiler: bpmncompiler.New(),
			})
			cache.EXPECT().SetNX(gomock.Any(), gomock.Any(), gomock.Any(), 30*time.Second).Return(true, nil)
			cache.EXPECT().DelIfEqual(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

			current := &domain.WorkflowVersion{ID: uuid.New(), WorkflowID: wfID, TenantID: tenantID, RecordVersion: 8}
			calls := []any{
				vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(&domain.WorkflowVersion{ID: current.ID, RecordVersion: 3}, nil),
				vRepo.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).Return(domain.ErrDraftConcurrency),
			}
			if tc.rereadErr == nil {
				calls = append(calls, vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(current, nil))
			} else {
				calls = append(calls, vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(nil, tc.rereadErr))
			}
			if errors.Is(tc.rereadErr, domain.ErrNotFound) {
				var wf *domain.Workflow
				if tc.workflowErr == nil {
					wf = &domain.Workflow{ID: wfID, TenantID: tenantID}
				}
				calls = append(calls, wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(wf, tc.workflowErr))
			}
			gomock.InOrder(calls...)

			newXML := "<bpmn/>"
			got, err := svc.Update(context.Background(), tenantID, userID, wfID, service.UpdateDraftReq{BPMNXML: &newXML, RecordVersion: 3})

			require.Error(t, err)
			assert.Nil(t, got)
			if tc.wantConflict {
				var conflict *domain.DraftConcurrencyError
				require.ErrorAs(t, err, &conflict)
				assert.Equal(t, int64(8), conflict.Current)
				assert.Empty(t, log.warns, "a re-read that succeeds logs nothing")
				return
			}
			assert.ErrorIs(t, err, tc.wantIs)
			assert.NotErrorIs(t, err, domain.ErrDraftConcurrency)
			var conflict *domain.DraftConcurrencyError
			assert.False(t, errors.As(err, &conflict), "no current record version is claimed when it could not be read")
			require.Len(t, log.warns, 1)
			assert.Equal(t, "draft re-read after a lost save failed", log.warns[0].msg)
			assert.Equal(t, tenantID.String(), log.warns[0].fields["tenant_id"])
			assert.Equal(t, wfID.String(), log.warns[0].fields["workflow_id"])
			assert.Contains(t, log.warns[0].fields["error"], tc.wantIs.Error())
		})
	}
}
