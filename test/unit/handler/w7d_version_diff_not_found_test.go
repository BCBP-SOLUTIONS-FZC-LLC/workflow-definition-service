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

func TestGetVersionDiff_VersionNotFoundOrOfAnotherWorkflow_Returns404NotFoundProblem(t *testing.T) {
	t.Parallel()
	otherWorkflowID := uuid.MustParse("66666666-6666-6666-6666-666666666666")
	cases := []struct {
		name    string
		arrange func(d *diffFailureDeps, baseID, targetID uuid.UUID)
	}{
		{
			name: "base version id does not exist",
			arrange: func(d *diffFailureDeps, baseID, _ uuid.UUID) {
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, baseID).Return(nil, domain.ErrNotFound)
			},
		},
		{
			name: "target version id does not exist",
			arrange: func(d *diffFailureDeps, baseID, targetID uuid.UUID) {
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, baseID).
					Return(diffFailureVersion(baseID, domain.VersionStatusDraft, diffFailureBaseXML), nil)
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, targetID).Return(nil, domain.ErrNotFound)
			},
		},
		{
			name: "base version belongs to another workflow",
			arrange: func(d *diffFailureDeps, baseID, targetID uuid.UUID) {
				base := diffFailureVersion(baseID, domain.VersionStatusDraft, diffFailureBaseXML)
				base.WorkflowID = otherWorkflowID
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, baseID).Return(base, nil)
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, targetID).
					Return(diffFailureVersion(targetID, domain.VersionStatusDraft, diffFailureTargetXML), nil)
			},
		},
		{
			name: "target version belongs to another workflow",
			arrange: func(d *diffFailureDeps, baseID, targetID uuid.UUID) {
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, baseID).
					Return(diffFailureVersion(baseID, domain.VersionStatusDraft, diffFailureBaseXML), nil)
				target := diffFailureVersion(targetID, domain.VersionStatusDraft, diffFailureTargetXML)
				target.WorkflowID = otherWorkflowID
				d.versions.EXPECT().GetByID(gomock.Any(), testTenantID, targetID).Return(target, nil)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, router := newDiffFailureRouter(t, false)
			baseID, targetID := uuid.New(), uuid.New()
			tc.arrange(d, baseID, targetID)

			w := do(router, req(http.MethodGet, diffFailurePath(baseID, targetID), nil))

			require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "NOT_FOUND", prob["code"])
			assert.Contains(t, prob["detail"], "resource not found")
			assert.NotContains(t, w.Body.String(), "invalid_params", "a missing version is not a validation problem")
		})
	}
}
