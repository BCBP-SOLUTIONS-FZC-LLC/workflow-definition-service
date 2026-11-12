package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestUpdateDraft_RealService_LostSaveRace_AnswerFollowsTheReread(t *testing.T) {
	cases := []struct {
		name        string
		rereadErr   error
		wantStatus  int
		wantCode    string
		wantVersion bool
	}{
		{name: "draft deleted meanwhile", rereadErr: domain.ErrNotFound, wantStatus: http.StatusNotFound, wantCode: "DRAFT_NOT_FOUND"},
		{name: "database fault on the re-read", rereadErr: domain.ErrUpstreamUnavailable, wantStatus: http.StatusServiceUnavailable, wantCode: "UPSTREAM_UNAVAILABLE"},
		{name: "re-read succeeds", wantStatus: http.StatusConflict, wantCode: "DRAFT_CONCURRENCY", wantVersion: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wfRepo, vRepo, router := newWiredDraftRouter(t)
			stored := &domain.WorkflowVersion{ID: testVerID, WorkflowID: testWFID, TenantID: testTenantID, RecordVersion: 3}
			calls := []any{
				vRepo.EXPECT().GetDraft(gomock.Any(), testTenantID, testWFID).Return(stored, nil),
				vRepo.EXPECT().UpdateDraft(gomock.Any(), gomock.Any()).Return(domain.ErrDraftConcurrency),
			}
			if tc.rereadErr == nil {
				calls = append(calls, vRepo.EXPECT().GetDraft(gomock.Any(), testTenantID, testWFID).
					Return(&domain.WorkflowVersion{ID: testVerID, WorkflowID: testWFID, TenantID: testTenantID, RecordVersion: 8}, nil))
			} else {
				calls = append(calls, vRepo.EXPECT().GetDraft(gomock.Any(), testTenantID, testWFID).Return(nil, tc.rereadErr))
			}
			if tc.rereadErr == domain.ErrNotFound {
				calls = append(calls, wfRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).
					Return(&domain.Workflow{ID: testWFID, TenantID: testTenantID}, nil))
			}
			gomock.InOrder(calls...)

			w := do(router, adminReq(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft",
				map[string]any{"bpmn_xml": "<bpmn/>", "record_version": 3}))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, tc.wantCode, prob["code"])
			if tc.wantVersion {
				assert.EqualValues(t, 8, prob["current_record_version"])
			} else {
				assert.NotContains(t, prob, "current_record_version")
			}
		})
	}
}
