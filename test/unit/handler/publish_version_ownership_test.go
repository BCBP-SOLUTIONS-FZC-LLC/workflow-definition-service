package handler_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestPublishVersion_RealService_VersionOfAnotherWorkflow_Returns404(t *testing.T) {
	otherWorkflowID := uuid.MustParse("77777777-7777-7777-7777-777777777777")
	cases := []struct {
		name   string
		status domain.VersionStatus
	}{
		{name: "published", status: domain.VersionStatusPublished},
		{name: "archived", status: domain.VersionStatusArchived},
		{name: "draft", status: domain.VersionStatusDraft},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vRepo, router := newWiredVersionRouter(t)
			vRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(
				&domain.WorkflowVersion{ID: testVerID, WorkflowID: otherWorkflowID, TenantID: testTenantID, Status: tc.status}, nil)

			w := do(router, adminReq(http.MethodPost, publishRequestTestPath(), nil))

			require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
			assert.Equal(t, "NOT_FOUND", decodeProblem(t, w)["code"])
		})
	}
}

func TestPublishVersion_RealService_PublishedVersionOfThisWorkflow_Returns409(t *testing.T) {
	vRepo, router := newWiredVersionRouter(t)
	vRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(
		&domain.WorkflowVersion{ID: testVerID, WorkflowID: testWFID, TenantID: testTenantID, Status: domain.VersionStatusPublished}, nil)

	w := do(router, adminReq(http.MethodPost, publishRequestTestPath(), nil))

	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Equal(t, "INVALID_VERSION_STATUS", decodeProblem(t, w)["code"])
}
