package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestProblemDetail_NotFoundAndConflictAnswersCarryAFixedDetailNotTheErrorChain(t *testing.T) {
	wfPath := "/api/v1/workflows/" + testWFID.String()
	verPath := wfPath + "/versions/" + testVerID.String()

	cases := []struct {
		name       string
		build      func(err error) *handler.Handler
		request    func() *http.Request
		err        error
		wantStatus int
		wantCode   string
		wantDetail string
	}{
		{
			name: "workflow not found",
			build: func(err error) *handler.Handler {
				return newHandler(&fakeWorkflowSvc{
					get: func(context.Context, uuid.UUID, uuid.UUID, int) (*domain.Workflow, []*domain.WorkflowVersion, error) {
						return nil, nil, err
					},
				}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
			},
			request:    func() *http.Request { return req(http.MethodGet, wfPath, nil) },
			err:        fmt.Errorf("get workflow: %w", domain.ErrNotFound),
			wantStatus: http.StatusNotFound,
			wantCode:   "NOT_FOUND",
			wantDetail: "resource not found",
		},
		{
			name: "draft missing",
			build: func(err error) *handler.Handler {
				return newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
					get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) { return nil, err },
				}, &fakeVersionSvc{}, &fakeValidationSvc{})
			},
			request:    func() *http.Request { return req(http.MethodGet, wfPath+"/draft", nil) },
			err:        fmt.Errorf("get draft version: %w", domain.ErrNoDraftExists),
			wantStatus: http.StatusNotFound,
			wantCode:   "DRAFT_NOT_FOUND",
			wantDetail: "no active draft exists",
		},
		{
			name: "draft already exists",
			build: func(err error) *handler.Handler {
				return newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
					init: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
						return nil, err
					},
				}, &fakeVersionSvc{}, &fakeValidationSvc{})
			},
			request:    func() *http.Request { return adminReq(http.MethodPost, wfPath+"/draft", nil) },
			err:        fmt.Errorf("init draft: insert version: %w", domain.ErrDraftAlreadyExists),
			wantStatus: http.StatusConflict,
			wantCode:   "DRAFT_ALREADY_EXISTS",
			wantDetail: "draft already exists",
		},
		{
			name: "duplicate template key on clone",
			build: func(err error) *handler.Handler {
				return newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
					clone: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string, service.CloneReq) (*domain.Workflow, *domain.WorkflowVersion, error) {
						return nil, nil, err
					},
				}, &fakeValidationSvc{})
			},
			request: func() *http.Request {
				return adminReq(http.MethodPost, verPath+"/clone", map[string]any{"new_key": "k", "new_name": "n"})
			},
			err:        fmt.Errorf("clone version: insert workflow: %w", domain.ErrDuplicateTemplateKey),
			wantStatus: http.StatusConflict,
			wantCode:   "DUPLICATE_TEMPLATE_KEY",
			wantDetail: "a workflow with this key already exists",
		},
		{
			name: "publish of a version that is not a draft",
			build: func(err error) *handler.Handler {
				return newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
					publish: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, bool) (*domain.WorkflowVersion, error) {
						return nil, err
					},
				}, &fakeValidationSvc{})
			},
			request:    func() *http.Request { return adminReq(http.MethodPost, verPath+"/publish", nil) },
			err:        fmt.Errorf("publish version: %w", domain.ErrVersionNotDraft),
			wantStatus: http.StatusConflict,
			wantCode:   "INVALID_VERSION_STATUS",
			wantDetail: "workflow version is not in DRAFT status",
		},
		{
			name: "publish with a structural divergence",
			build: func(err error) *handler.Handler {
				return newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
					publish: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, bool) (*domain.WorkflowVersion, error) {
						return nil, err
					},
				}, &fakeValidationSvc{})
			},
			request:    func() *http.Request { return adminReq(http.MethodPost, verPath+"/publish", nil) },
			err:        fmt.Errorf("active version no longer compiles: %w", domain.ErrStructuralDivergence),
			wantStatus: http.StatusConflict,
			wantCode:   "STRUCTURAL_DIVERGENCE",
			wantDetail: "structural divergence detected",
		},
		{
			name: "publish with active instances",
			build: func(err error) *handler.Handler {
				return newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
					publish: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, bool) (*domain.WorkflowVersion, error) {
						return nil, err
					},
				}, &fakeValidationSvc{})
			},
			request:    func() *http.Request { return adminReq(http.MethodPost, verPath+"/publish", nil) },
			err:        fmt.Errorf("count active instances: %w", domain.ErrActiveInstancesExist),
			wantStatus: http.StatusConflict,
			wantCode:   "ACTIVE_INSTANCES_EXIST",
			wantDetail: "active workflow instances exist",
		},
		{
			name: "no active version",
			build: func(err error) *handler.Handler {
				return newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
					promote: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string) (*domain.WorkflowVersion, error) {
						return nil, err
					},
				}, &fakeValidationSvc{})
			},
			request:    func() *http.Request { return adminReq(http.MethodPost, verPath+"/promote", nil) },
			err:        fmt.Errorf("resolve active version: %w", domain.ErrNoActiveVersion),
			wantStatus: http.StatusConflict,
			wantCode:   "NO_ACTIVE_VERSION",
			wantDetail: "no active version",
		},
		{
			name: "module process id taken",
			build: func(err error) *handler.Handler {
				return newModuleHandler(&fakeModuleSvc{
					create: func(context.Context, uuid.UUID, uuid.UUID, service.CreateModuleReq) (*domain.Module, *domain.ModuleVersion, error) {
						return nil, nil, err
					},
				})
			},
			request: func() *http.Request {
				return adminReq(http.MethodPost, "/api/v1/modules", map[string]any{"name": "m", "bpmn_xml": "<bpmn/>"})
			},
			err:        fmt.Errorf("create module: process id %q: %w", "ApprovalChain", domain.ErrModuleProcessIDTaken),
			wantStatus: http.StatusConflict,
			wantCode:   "MODULE_PROCESS_ID_CONFLICT",
			wantDetail: "module process id is already in use",
		},
		{
			name: "module version changes the process id",
			build: func(err error) *handler.Handler {
				return newModuleHandler(&fakeModuleSvc{
					get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
						return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
					},
					addVersion: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.AddModuleVersionReq) (*domain.ModuleVersion, error) {
						return nil, err
					},
				})
			},
			request: func() *http.Request {
				return adminReq(http.MethodPost, "/api/v1/modules/"+testWFID.String()+"/versions", map[string]any{"bpmn_xml": "<bpmn/>"})
			},
			err:        fmt.Errorf("process id %q, module keeps %q: %w", "A", "B", domain.ErrModuleProcessIDChanged),
			wantStatus: http.StatusConflict,
			wantCode:   "MODULE_PROCESS_ID_CONFLICT",
			wantDetail: "module version must keep its module's process id",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := do(newRouter(tc.build(tc.err)), tc.request())

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, tc.wantCode, prob["code"])
			assert.Equal(t, tc.wantDetail, prob["detail"])
		})
	}
}

func TestProblemDetail_DraftConcurrencyKeepsTheCurrentVersionButDropsTheChain(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{
		update: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
			return nil, fmt.Errorf("update draft: %w", &domain.DraftConcurrencyError{Current: 7})
		},
	}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), adminReq(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft", map[string]any{"name": "x", "record_version": 3}))

	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	prob := decodeProblem(t, w)
	assert.Equal(t, "DRAFT_CONCURRENCY", prob["code"])
	assert.Equal(t, "draft concurrency violation", prob["detail"])
	assert.EqualValues(t, 7, prob["current_record_version"])
}
