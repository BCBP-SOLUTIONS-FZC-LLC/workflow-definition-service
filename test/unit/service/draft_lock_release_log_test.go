package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"

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

type lockReleaseLog struct {
	testkit.FakeLogger
	mu    sync.Mutex
	warns []draftRaceLogEntry
}

func (l *lockReleaseLog) Warn(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, draftRaceLogEntry{msg: msg, fields: fields})
}

func TestDraftService_Update_FailedLockRelease_LogsAWarningNamingTheLockAndStillSucceeds(t *testing.T) {
	ctrl := gomock.NewController(t)
	cache := mocks.NewMockCacheStore(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	log := &lockReleaseLog{}
	svc := service.NewDraftService(service.DraftDeps{
		Cache: cache, Workflows: mocks.NewMockWorkflowRepository(ctrl), Versions: vRepo, Log: log, Compiler: bpmncompiler.New(),
	})
	tenantID, userID, wfID := uuid.New(), uuid.New(), uuid.New()
	wantKey := "draft-lock:" + tenantID.String() + ":" + wfID.String()
	cache.EXPECT().SetNX(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
	cache.EXPECT().DelIfEqual(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("valkey: connection refused"))
	vRepo.EXPECT().GetDraft(gomock.Any(), tenantID, wfID).Return(&domain.WorkflowVersion{ID: uuid.New(), RecordVersion: 1}, nil)
	vRepo.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).Return(nil)

	_, err := svc.Update(context.Background(), tenantID, userID, wfID, service.UpdateDraftReq{RecordVersion: 1})

	require.NoError(t, err, "the lock expires by itself, so the save still succeeds")
	require.Len(t, log.warns, 1)
	assert.Equal(t, "draft lock release failed", log.warns[0].msg)
	assert.Equal(t, wantKey, log.warns[0].fields["lock_key"])
	assert.Contains(t, log.warns[0].fields["error"], "connection refused")
}
