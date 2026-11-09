package handler_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

func versionValidationPath(workflowID, versionID string) string {
	return "/api/v1/workflows/" + workflowID + "/versions/" + versionID + "/validation"
}

func TestGetVersionValidation_MalformedPathIDs_Returns400(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		workflowID string
		versionID  string
		wantDetail string
	}{
		{"workflow id is not a UUID", "not-a-uuid", testVerID.String(), "invalid id: not a valid UUID"},
		{"version id is not a UUID", testWFID.String(), "not-a-uuid", "invalid version_id: not a valid UUID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reads := 0
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
				get: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
					reads++
					return nil, nil
				},
			}, &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, versionValidationPath(tc.workflowID, tc.versionID), nil))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, tc.wantDetail, prob["detail"])
			assert.Zero(t, reads, "a malformed id must not reach the version lookup")
		})
	}
}

func TestGetVersionValidation_NoIdentityInContext_Returns500(t *testing.T) {
	t.Parallel()
	reads := 0
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		get: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
			reads++
			return nil, nil
		},
	}, &fakeValidationSvc{})

	w := do(newBareRouter(h), req(http.MethodGet, versionValidationPath(testWFID.String(), testVerID.String()), nil))

	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	prob := decodeProblem(t, w)
	assert.Equal(t, "INTERNAL_ERROR", prob["code"])
	assert.Equal(t, "missing request context", prob["detail"])
	assert.Zero(t, reads, "without an identity nothing is read")
}

type versionValidationFixture struct {
	compiler *mocks.MockPlanCompiler
	modules  *mocks.MockModuleVersionRepository
}

func TestGetVersionValidation_ModuleResolution_ReportsThroughRealValidationService(t *testing.T) {
	t.Parallel()
	pinnedVersionID := uuid.New()
	call := domain.ModuleCall{CallID: "CA_1", ProcessID: "Process_Mod", VersionNumber: 2}
	pinnedRefs := []domain.ModuleRef{{
		ModuleID: uuid.New(), VersionID: pinnedVersionID, ProcessID: "Process_Mod", VersionNumber: 2,
	}}

	cases := []struct {
		name       string
		version    *domain.WorkflowVersion
		setup      func(f versionValidationFixture)
		wantStatus int
		wantCode   string
		wantIssues []bpmnIssueWant
	}{
		{
			name:    "pinned module version hidden from the tenant is an unresolved call",
			version: &domain.WorkflowVersion{ID: testVerID, Status: domain.VersionStatusPublished, BPMNXML: "<main/>", ModuleRefs: pinnedRefs},
			setup: func(f versionValidationFixture) {
				f.compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{call}, nil, nil)
				f.modules.EXPECT().GetByID(gomock.Any(), testTenantID, pinnedVersionID).Return(nil, domain.ErrNotFound)
			},
			wantStatus: http.StatusOK,
			wantIssues: []bpmnIssueWant{{
				code: string(domain.BPMNErrUnresolvedCalledElement), nodeID: "CA_1", severity: string(domain.SeverityError),
			}},
		},
		{
			name:    "live module lookup that cannot reach the database is 503",
			version: &domain.WorkflowVersion{ID: testVerID, Status: domain.VersionStatusDraft, BPMNXML: "<main/>"},
			setup: func(f versionValidationFixture) {
				f.compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{call}, nil, nil)
				f.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Mod", int32(2)).
					Return(nil, fmt.Errorf("query: %w", domain.ErrUpstreamUnavailable))
			},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
		},
		{
			name:    "pinned module lookup that cannot reach the database is 503",
			version: &domain.WorkflowVersion{ID: testVerID, Status: domain.VersionStatusPublished, BPMNXML: "<main/>", ModuleRefs: pinnedRefs},
			setup: func(f versionValidationFixture) {
				f.compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{call}, nil, nil)
				f.modules.EXPECT().GetByID(gomock.Any(), testTenantID, pinnedVersionID).
					Return(nil, fmt.Errorf("query: %w", domain.ErrUpstreamUnavailable))
			},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
		},
		{
			name:    "any other module lookup failure is 500",
			version: &domain.WorkflowVersion{ID: testVerID, Status: domain.VersionStatusDraft, BPMNXML: "<main/>"},
			setup: func(f versionValidationFixture) {
				f.compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{call}, nil, nil)
				f.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Mod", int32(2)).
					Return(nil, errors.New("constraint violation"))
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
		},
		{
			name:    "malformed call reported by the compiler keeps its node and message",
			version: &domain.WorkflowVersion{ID: testVerID, Status: domain.VersionStatusDraft, BPMNXML: "<main/>"},
			setup: func(f versionValidationFixture) {
				f.compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return(nil, []domain.BPMNValidationError{{
					Code: domain.BPMNErrUnresolvedCalledElement, NodeID: "CA_9",
					Message:  `call "Review" pins versionTag "abc"; it must be a published module version number`,
					Severity: domain.SeverityError,
				}}, nil)
			},
			wantStatus: http.StatusOK,
			wantIssues: []bpmnIssueWant{{
				code: string(domain.BPMNErrUnresolvedCalledElement), nodeID: "CA_9", severity: string(domain.SeverityError),
				message: `call "Review" pins versionTag "abc"; it must be a published module version number`,
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			f := versionValidationFixture{
				compiler: mocks.NewMockPlanCompiler(ctrl),
				modules:  mocks.NewMockModuleVersionRepository(ctrl),
			}
			tc.setup(f)
			validation := service.NewValidationService(service.ValidationDeps{Compiler: f.compiler, ModuleVersions: f.modules})
			versions := &fakeVersionSvc{
				get: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
					return tc.version, nil
				},
			}
			h := handler.New(handler.Services{Versions: versions, Validation: validation})

			w := do(newRouter(h), req(http.MethodGet, versionValidationPath(testWFID.String(), testVerID.String()), nil))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			if tc.wantStatus != http.StatusOK {
				assert.Equal(t, tc.wantCode, decodeProblem(t, w)["code"])
				return
			}
			body := decodeProblem(t, w)
			assert.Equal(t, false, body["is_valid"])
			assert.Equal(t, tc.version.ID.String(), body["version_id"])
			assert.Equal(t, string(tc.version.Status), body["status"])
			assertBPMNIssues(t, body, tc.wantIssues)
		})
	}
}

type bpmnIssueWant struct {
	code, nodeID, severity, message string
}

func assertBPMNIssues(t *testing.T, body map[string]any, want []bpmnIssueWant) {
	t.Helper()
	issues, _ := body["issues"].([]any)
	require.Len(t, issues, len(want))
	for i, w := range want {
		got := issues[i].(map[string]any)
		assert.Equal(t, w.code, got["code"])
		assert.Equal(t, w.nodeID, got["node_id"])
		assert.Equal(t, w.severity, got["severity"])
		if w.message != "" {
			assert.Equal(t, w.message, got["message"])
		}
	}
}
