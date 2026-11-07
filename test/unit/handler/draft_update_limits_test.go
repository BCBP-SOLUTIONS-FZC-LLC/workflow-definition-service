package handler_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestUpdateDraft_NameAndDescriptionLengthLimits(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		fieldName   string
		fieldDesc   string
		wantStatus  int
		wantInvalid []string
		wantReasons []string
	}{
		{
			name:        "name of 256 characters is refused",
			fieldName:   strings.Repeat("a", 256),
			wantStatus:  http.StatusUnprocessableEntity,
			wantInvalid: []string{"name"},
			wantReasons: []string{"must not exceed 255 characters"},
		},
		{
			name:        "description of 2001 characters is refused",
			fieldDesc:   strings.Repeat("d", 2001),
			wantStatus:  http.StatusUnprocessableEntity,
			wantInvalid: []string{"description"},
			wantReasons: []string{"must not exceed 2000 characters"},
		},
		{
			name:        "name and description over their limits are both named",
			fieldName:   strings.Repeat("a", 256),
			fieldDesc:   strings.Repeat("d", 2001),
			wantStatus:  http.StatusUnprocessableEntity,
			wantInvalid: []string{"name", "description"},
			wantReasons: []string{"must not exceed 255 characters", "must not exceed 2000 characters"},
		},
		{
			name:       "name of exactly 255 and description of exactly 2000 characters is accepted",
			fieldName:  strings.Repeat("a", 255),
			fieldDesc:  strings.Repeat("d", 2000),
			wantStatus: http.StatusOK,
		},
		{
			name:       "name of 255 multi-byte characters is accepted although it is over 255 bytes",
			fieldName:  strings.Repeat("世", 255),
			wantStatus: http.StatusOK,
		},
		{
			name:        "name of 256 multi-byte characters is refused",
			fieldName:   strings.Repeat("世", 256),
			wantStatus:  http.StatusUnprocessableEntity,
			wantInvalid: []string{"name"},
			wantReasons: []string{"must not exceed 255 characters"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got *service.UpdateDraftReq
			var gotTenant, gotUser, gotWorkflow uuid.UUID
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
				update: func(_ context.Context, tenantID, userID, workflowID uuid.UUID, r service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
					got = &r
					gotTenant, gotUser, gotWorkflow = tenantID, userID, workflowID
					return newDraftVersion(), nil
				},
			}, &fakeVersionSvc{}, &fakeValidationSvc{})

			body := map[string]any{"record_version": 3}
			if tc.fieldName != "" {
				body["name"] = tc.fieldName
			}
			if tc.fieldDesc != "" {
				body["description"] = tc.fieldDesc
			}
			w := do(newRouter(h), adminReq(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft", body))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			if tc.wantStatus == http.StatusOK {
				require.NotNil(t, got, "an accepted request reaches the draft service")
				assert.Equal(t, testTenantID, gotTenant)
				assert.Equal(t, testUserID, gotUser)
				assert.Equal(t, testWFID, gotWorkflow)
				assert.EqualValues(t, 3, got.RecordVersion)
				if tc.fieldName != "" {
					require.NotNil(t, got.Name)
					assert.Equal(t, tc.fieldName, *got.Name)
				}
				if tc.fieldDesc != "" {
					require.NotNil(t, got.Description)
					assert.Equal(t, tc.fieldDesc, *got.Description)
				}
				return
			}

			assert.Nil(t, got, "a refused request never reaches the draft service")
			prob := decodeProblem(t, w)
			assert.Equal(t, "INVALID_INPUT", prob["code"])
			params, ok := prob["invalid_params"].([]any)
			require.True(t, ok, "invalid_params present: %v", prob)
			require.Len(t, params, len(tc.wantInvalid))
			for i, p := range params {
				entry := p.(map[string]any)
				assert.Equal(t, tc.wantInvalid[i], entry["name"])
				assert.Equal(t, tc.wantReasons[i], entry["reason"])
			}
		})
	}
}

func TestUpdateDraft_UnexpectedFailure_Returns500WithoutLeakingTheCause(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{name: "an unclassified database error", err: errors.New("update draft: pq: disk exploded at /var/lib/pg")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
				update: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
					return nil, tc.err
				},
			}, &fakeVersionSvc{}, &fakeValidationSvc{})

			w := do(newRouter(h), adminReq(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft",
				map[string]any{"name": "x", "record_version": 1}))

			require.Equal(t, http.StatusInternalServerError, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "INTERNAL_ERROR", prob["code"])
			assert.Equal(t, "an unexpected error occurred", prob["detail"])
			assert.NotContains(t, w.Body.String(), "disk exploded")
		})
	}
}
