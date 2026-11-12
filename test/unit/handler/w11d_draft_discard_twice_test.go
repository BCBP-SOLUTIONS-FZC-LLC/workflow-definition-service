package handler_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestDraftRoutes_RealService_DiscardRepeated_AnswersNotFoundWithDraftNotFoundCode(t *testing.T) {
	wfRepo, vRepo, router := newWiredDraftRouter(t)
	draftID := uuid.New()
	stored := &domain.WorkflowVersion{ID: draftID, WorkflowID: testWFID, TenantID: testTenantID}
	vRepo.EXPECT().GetDraft(gomock.Any(), testTenantID, testWFID).DoAndReturn(
		func(context.Context, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
			if stored == nil {
				return nil, domain.ErrNotFound
			}
			return stored, nil
		}).Times(2)
	vRepo.EXPECT().DeleteDraft(gomock.Any(), testTenantID, draftID).DoAndReturn(
		func(context.Context, uuid.UUID, uuid.UUID) error {
			stored = nil
			return nil
		}).Times(1)
	wfRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(&domain.Workflow{ID: testWFID, TenantID: testTenantID}, nil).Times(1)
	path := "/api/v1/workflows/" + testWFID.String() + "/draft"

	first := do(router, adminReq(http.MethodDelete, path, nil))
	second := do(router, adminReq(http.MethodDelete, path, nil))

	require.Equal(t, http.StatusNoContent, first.Code, first.Body.String())
	require.Equal(t, http.StatusNotFound, second.Code, second.Body.String())
	assert.Equal(t, "DRAFT_NOT_FOUND", decodeProblem(t, second)["code"])
}
