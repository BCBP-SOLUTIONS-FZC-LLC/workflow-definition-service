package handler_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestGetWorkflow_RealService_BodyShape(t *testing.T) {
	t.Parallel()

	publishedAt := time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC)
	createdAt := time.Date(2026, 2, 1, 8, 0, 0, 0, time.UTC)
	author := uuid.MustParse("66666666-6666-6666-6666-666666666666")
	activeID := uuid.MustParse("77777777-7777-7777-7777-777777777777")
	draftID := uuid.MustParse("88888888-8888-8888-8888-888888888888")
	one, two := int32(1), int32(2)

	published := newPublishedVersion()
	published.ID = activeID
	published.VersionNumber = &two
	published.PublishedAt = &publishedAt
	published.CreatedAt = createdAt
	published.CreatedByUserID = author
	older := newPublishedVersion()
	older.ID = testVerID2
	older.Status = domain.VersionStatusArchived
	older.VersionNumber = &one
	older.CreatedAt = createdAt.Add(-time.Hour)
	older.CreatedByUserID = author
	draft := newDraftVersion()
	draft.ID = draftID
	draft.IsValid = false
	draft.CreatedAt = createdAt.Add(time.Hour)
	draft.CreatedByUserID = author

	cases := []struct {
		name       string
		workflow   func() *domain.Workflow
		stored     []*domain.WorkflowVersion
		wantActive bool
		wantIDs    []string
	}{
		{
			name: "active version with several versions",
			workflow: func() *domain.Workflow {
				wf := newWorkflow()
				wf.ActiveVersionID = &activeID
				wf.ActiveVersionNumber = &two
				return wf
			},
			stored:     []*domain.WorkflowVersion{draft, published, older},
			wantActive: true,
			wantIDs:    []string{draftID.String(), activeID.String(), testVerID2.String()},
		},
		{
			name:     "draft only has no active version keys",
			workflow: newWorkflow,
			stored:   []*domain.WorkflowVersion{draft},
			wantIDs:  []string{draftID.String()},
		},
		{
			name:     "no version rows gives an empty array",
			workflow: newWorkflow,
			stored:   nil,
			wantIDs:  []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wfRepo, vRepo, router := newWiredWorkflowRouter(t)
			wf := tc.workflow()
			wfRepo.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(wf, nil)
			vRepo.EXPECT().ListByWorkflow(gomock.Any(), testTenantID, testWFID, 1, 20).
				Return(tc.stored, int64(len(tc.stored)), nil)

			w := do(router, req(http.MethodGet, "/api/v1/workflows/"+testWFID.String(), nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			body := decodeProblem(t, w)
			assert.Equal(t, testWFID.String(), body["id"])
			assert.Equal(t, wf.TemplateKey, body["key"])
			assert.Equal(t, wf.Name, body["name"])
			if tc.wantActive {
				assert.Equal(t, activeID.String(), body["active_version_id"])
				assert.EqualValues(t, 2, body["active_version_number"])
			} else {
				assert.NotContains(t, body, "active_version_id")
				assert.NotContains(t, body, "active_version_number")
			}

			versions, isList := body["versions"].([]any)
			require.True(t, isList, "versions must encode as an array, got %T", body["versions"])
			gotIDs := make([]string, 0, len(versions))
			for _, item := range versions {
				entry, isObject := item.(map[string]any)
				require.True(t, isObject)
				id, isString := entry["id"].(string)
				require.True(t, isString)
				gotIDs = append(gotIDs, id)
			}
			assert.Equal(t, tc.wantIDs, gotIDs, "versions keep the order the repository returned")

			if len(versions) == 0 {
				return
			}
			first := versions[0].(map[string]any)
			assert.Equal(t, string(domain.VersionStatusDraft), first["status"])
			assert.NotContains(t, first, "version_number")
			assert.NotContains(t, first, "published_at")
			assert.Equal(t, false, first["is_valid"])
			assert.Equal(t, author.String(), first["created_by_user_id"])
			assert.Equal(t, draft.CreatedAt.UTC().Format(time.RFC3339Nano), first["created_at"])
			if tc.wantActive {
				second := versions[1].(map[string]any)
				assert.Equal(t, string(domain.VersionStatusPublished), second["status"])
				assert.EqualValues(t, 2, second["version_number"])
				assert.Equal(t, publishedAt.Format(time.RFC3339Nano), second["published_at"])
				assert.NotContains(t, second, "bpmn_xml")
				assert.NotContains(t, second, "compiled_plan_json")
			}
		})
	}
}
