package handler_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestInitDraft_RepositoryFailure_MapsToProblemStatus(t *testing.T) {
	t.Parallel()

	unreachable := fmt.Errorf("query: %w: %w", domain.ErrUpstreamUnavailable, errors.New("connection refused"))
	unclassified := errors.New("pq: disk full at /var/lib/postgres")

	cases := []struct {
		name       string
		failAt     string
		failure    error
		wantStatus int
		wantCode   string
		wantType   string
		wantDetail string
	}{
		{
			name: "workflow lookup unreachable", failAt: "workflow", failure: unreachable,
			wantStatus: http.StatusServiceUnavailable, wantCode: "UPSTREAM_UNAVAILABLE",
			wantType: "service-unavailable", wantDetail: "an upstream dependency is temporarily unavailable",
		},
		{
			name: "active version lookup unreachable", failAt: "active", failure: unreachable,
			wantStatus: http.StatusServiceUnavailable, wantCode: "UPSTREAM_UNAVAILABLE",
			wantType: "service-unavailable", wantDetail: "an upstream dependency is temporarily unavailable",
		},
		{
			name: "draft insert unreachable", failAt: "insert", failure: unreachable,
			wantStatus: http.StatusServiceUnavailable, wantCode: "UPSTREAM_UNAVAILABLE",
			wantType: "service-unavailable", wantDetail: "an upstream dependency is temporarily unavailable",
		},
		{
			name: "draft insert unclassified", failAt: "insert", failure: unclassified,
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_ERROR",
			wantType: "internal-error", wantDetail: "an unexpected error occurred",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)

			activeID := uuid.New()
			if tc.failAt == "workflow" {
				wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, tc.failure)
			} else {
				wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&domain.Workflow{ActiveVersionID: &activeID}, nil)
			}
			switch tc.failAt {
			case "active":
				vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), activeID).Return(nil, tc.failure)
			case "insert":
				vRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), activeID).
					Return(&domain.WorkflowVersion{BPMNXML: "<bpmn/>"}, nil)
				vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(tc.failure)
			}

			drafts := service.NewDraftService(service.DraftDeps{Workflows: wfRepo, Versions: vRepo})
			h := handler.New(handler.Services{Drafts: drafts})

			w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/workflows/"+testWFID.String()+"/draft", nil))

			require.Equal(t, tc.wantStatus, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, tc.wantCode, prob["code"])
			assert.True(t, strings.HasSuffix(prob["type"].(string), "/"+tc.wantType), "type = %v", prob["type"])
			assert.Equal(t, tc.wantDetail, prob["detail"])
			assert.NotContains(t, w.Body.String(), "disk full")
			assert.NotContains(t, w.Body.String(), "connection refused")
		})
	}
}
