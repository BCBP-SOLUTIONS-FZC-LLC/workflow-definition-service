package handler_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const (
	w7dCreatePath = "/api/v1/workflows"
	w7dMainXML    = "<w7d-main-definitions/>"
	w7dBundledXML = "<w7d-bundled-definitions/>"
)

func w7dTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "service", "testdata", name))
	require.NoError(t, err)
	return string(b)
}

func w7dCreateBody(bpmnXML string) map[string]any {
	return map[string]any{"key": "w7d-key", "name": "W7D", "bpmn_xml": bpmnXML}
}

func w7dInvalidParams(t *testing.T, prob map[string]any) []map[string]any {
	t.Helper()
	raw, ok := prob["invalid_params"].([]any)
	require.True(t, ok, "problem body carries invalid_params: %v", prob)
	out := make([]map[string]any, len(raw))
	for i, p := range raw {
		out[i], ok = p.(map[string]any)
		require.True(t, ok)
	}
	return out
}

type w7dCreateWiring struct {
	modules  *mocks.MockModuleVersionRepository
	compiler *mocks.MockPlanCompiler
	router   *gin.Engine
}

func w7dNewCreateWiring(t *testing.T, realCompiler bool, limits port.PlanLimits) *w7dCreateWiring {
	t.Helper()
	ctrl := gomock.NewController(t)
	w := &w7dCreateWiring{
		modules:  mocks.NewMockModuleVersionRepository(ctrl),
		compiler: mocks.NewMockPlanCompiler(ctrl),
	}
	deps := service.WorkflowDeps{
		Transactor:     mocks.NewMockTransactor(ctrl),
		Workflows:      mocks.NewMockWorkflowRepository(ctrl),
		Versions:       mocks.NewMockWorkflowVersionRepository(ctrl),
		ModuleVersions: w.modules,
		PlanLimits:     limits,
	}
	if realCompiler {
		deps.Compiler = bpmn_compiler.New()
	} else {
		deps.Compiler = w.compiler
	}
	w.router = newRouter(handler.New(handler.Services{Workflows: service.NewWorkflowService(deps)}))
	return w
}

func w7dModuleVersion(processID string, number int32, bpmnXML string) *domain.ModuleVersion {
	return &domain.ModuleVersion{
		ID: uuid.New(), ModuleID: uuid.New(), ProcessID: processID, VersionNumber: &number,
		Status: domain.VersionStatusPublished, BPMNXML: bpmnXML,
	}
}

func TestCreateWorkflow_CallToAnUnresolvableModule_Returns422OnTheCallActivity(t *testing.T) {
	t.Parallel()
	caller := w7dTestdata(t, "library-caller.bpmn")
	review := w7dTestdata(t, "library-module-review.bpmn")
	cases := []struct {
		name       string
		pinned     *domain.ModuleVersion
		latest     *domain.ModuleVersion
		wantCallID string
		wantReason string
	}{
		{
			name:       "no module of that process id exists for the tenant",
			pinned:     w7dModuleVersion("Process_Review", 3, review),
			wantCallID: "CA_2",
			wantReason: `call names module "Process_Review", which is not a published, unarchived module this tenant can call`,
		},
		{
			name:       "the pinned module version does not exist",
			latest:     w7dModuleVersion("Process_Review", 4, review),
			wantCallID: "CA_1",
			wantReason: `call names module "Process_Review" version 3, which does not exist or was never published, or the module is archived`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := w7dNewCreateWiring(t, true, nil)
			lookup := func(version int32, found *domain.ModuleVersion) {
				if found == nil {
					w.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Review", version).Return(nil, domain.ErrNotFound)
					return
				}
				w.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Review", version).Return(found, nil)
			}
			lookup(3, tc.pinned)
			lookup(0, tc.latest)

			resp := do(w.router, adminReq(http.MethodPost, w7dCreatePath, w7dCreateBody(caller)))

			require.Equal(t, http.StatusUnprocessableEntity, resp.Code, resp.Body.String())
			prob := decodeProblem(t, resp)
			assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
			params := w7dInvalidParams(t, prob)
			require.Len(t, params, 1)
			assert.Equal(t, map[string]any{
				"name": tc.wantCallID, "code": "UNRESOLVED_CALLED_ELEMENT", "reason": tc.wantReason,
			}, params[0])
		})
	}
}

func TestCreateWorkflow_ModuleCallingAMissingModule_Returns422OnTheWorkflowsOwnCall(t *testing.T) {
	t.Parallel()
	caller := w7dTestdata(t, "library-caller.bpmn")
	reviewCallsCheck := w7dTestdata(t, "library-module-review-calls-check.bpmn")
	w := w7dNewCreateWiring(t, true, nil)
	review := w7dModuleVersion("Process_Review", 3, reviewCallsCheck)
	w.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Review", int32(3)).Return(review, nil)
	w.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Review", int32(0)).Return(review, nil)
	w.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Check", int32(0)).Return(nil, domain.ErrNotFound)

	resp := do(w.router, adminReq(http.MethodPost, w7dCreatePath, w7dCreateBody(caller)))

	require.Equal(t, http.StatusUnprocessableEntity, resp.Code, resp.Body.String())
	prob := decodeProblem(t, resp)
	assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
	params := w7dInvalidParams(t, prob)
	require.NotEmpty(t, params)
	assert.Equal(t, "CA_1", params[0]["name"], "the problem is reported on the call in the submitted workflow")
	assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", params[0]["code"])
	assert.Equal(t,
		`inside module "Process_Review" version 3: call names module "Process_Check", which is not a published, unarchived module this tenant can call`,
		params[0]["reason"])
}

func TestCreateWorkflow_CompilerOutcomes_MapToTheirStatuses(t *testing.T) {
	t.Parallel()
	const leaked = "zzq compiler internals"
	cases := []struct {
		name       string
		arrange    func(c *mocks.MockPlanCompiler)
		wantStatus int
		wantCode   string
		wantDetail string
	}{
		{
			name: "bundling the resolved modules fails",
			arrange: func(c *mocks.MockPlanCompiler) {
				c.EXPECT().ModuleCalls(gomock.Any(), w7dMainXML).Return(nil, nil, nil)
				c.EXPECT().BundleModules(w7dMainXML, []domain.ResolvedModule(nil)).Return("", errors.New(leaked))
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantDetail: "an unexpected error occurred",
		},
		{
			name: "the validator fails with an error that is not a finding",
			arrange: func(c *mocks.MockPlanCompiler) {
				c.EXPECT().ModuleCalls(gomock.Any(), w7dMainXML).Return(nil, nil, nil)
				c.EXPECT().BundleModules(w7dMainXML, []domain.ResolvedModule(nil)).Return(w7dBundledXML, nil)
				c.EXPECT().Validate(gomock.Any(), w7dBundledXML).Return(nil, errors.New(leaked))
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantDetail: "an unexpected error occurred",
		},
		{
			name: "the validator reports the document as malformed",
			arrange: func(c *mocks.MockPlanCompiler) {
				c.EXPECT().ModuleCalls(gomock.Any(), w7dMainXML).Return(nil, nil, nil)
				c.EXPECT().BundleModules(w7dMainXML, []domain.ResolvedModule(nil)).Return(w7dBundledXML, nil)
				c.EXPECT().Validate(gomock.Any(), w7dBundledXML).Return(nil, domain.ErrMalformedBPMN)
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_BPMN_XML",
			wantDetail: "the BPMN document could not be parsed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := w7dNewCreateWiring(t, false, nil)
			tc.arrange(w.compiler)

			resp := do(w.router, adminReq(http.MethodPost, w7dCreatePath, w7dCreateBody(w7dMainXML)))

			require.Equal(t, tc.wantStatus, resp.Code, resp.Body.String())
			prob := decodeProblem(t, resp)
			assert.Equal(t, tc.wantCode, prob["code"])
			assert.Equal(t, tc.wantDetail, prob["detail"])
			assert.NotContains(t, resp.Body.String(), leaked)
		})
	}
}

func TestCreateWorkflow_WarningsAndErrorsTogether_AreAllListedInTheValidationProblem(t *testing.T) {
	t.Parallel()
	findings := []domain.BPMNValidationError{
		{Code: domain.BPMNErrorCode("W7D_UNUSED_LANE"), NodeID: "Lane_1", Message: "lane has no nodes", Severity: domain.SeverityWarning},
		{Code: domain.BPMNErrorCode("W7D_NO_END"), NodeID: "Process_1", Message: "process has no end event", Severity: domain.SeverityError},
		{Code: domain.BPMNErrorCode("W7D_NAMELESS_TASK"), NodeID: "Task_1", Message: "task has no name", Severity: domain.SeverityWarning},
	}
	w := w7dNewCreateWiring(t, false, nil)
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), w7dMainXML).Return(nil, nil, nil)
	w.compiler.EXPECT().BundleModules(w7dMainXML, []domain.ResolvedModule(nil)).Return(w7dBundledXML, nil)
	w.compiler.EXPECT().Validate(gomock.Any(), w7dBundledXML).Return(findings, nil)

	resp := do(w.router, adminReq(http.MethodPost, w7dCreatePath, w7dCreateBody(w7dMainXML)))

	require.Equal(t, http.StatusUnprocessableEntity, resp.Code, resp.Body.String())
	prob := decodeProblem(t, resp)
	assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
	assert.Equal(t, []map[string]any{
		{"name": "Lane_1", "code": "W7D_UNUSED_LANE", "reason": "lane has no nodes"},
		{"name": "Process_1", "code": "W7D_NO_END", "reason": "process has no end event"},
		{"name": "Task_1", "code": "W7D_NAMELESS_TASK", "reason": "task has no name"},
	}, w7dInvalidParams(t, prob))
}

type w7dPlanTableReader struct {
	table   map[string]*int64
	err     error
	release chan struct{}
}

func (r *w7dPlanTableReader) ReadPlanTable(ctx context.Context) (map[string]*int64, error) {
	if r.release != nil {
		select {
		case <-r.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return r.table, r.err
}

func TestCreateWorkflow_PlanTableUnavailable_Returns503WithoutWritingAnything(t *testing.T) {
	t.Parallel()
	const leaked = "zzq plan table backend refused"
	expired := func(t *testing.T) context.Context {
		t.Helper()
		ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
		t.Cleanup(cancel)
		return ctx
	}
	cases := []struct {
		name    string
		reader  func(t *testing.T) *w7dPlanTableReader
		context func(t *testing.T) context.Context
	}{
		{
			name:    "the plan table source returns an error",
			reader:  func(*testing.T) *w7dPlanTableReader { return &w7dPlanTableReader{err: errors.New(leaked)} },
			context: func(*testing.T) context.Context { return context.Background() },
		},
		{
			name: "the plan table read outlasts the request deadline",
			reader: func(t *testing.T) *w7dPlanTableReader {
				r := &w7dPlanTableReader{release: make(chan struct{})}
				t.Cleanup(func() { close(r.release) })
				return r
			},
			context: expired,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			limits := service.NewPlanLimitService(service.PlanLimitDeps{Reader: tc.reader(t), TTL: time.Hour})
			w := w7dNewCreateWiring(t, false, limits)
			r := adminReq(http.MethodPost, w7dCreatePath, map[string]any{"key": "w7d-key", "name": "W7D", "bpmn_xml": w7dMainXML})
			r.Header.Set("x-plan", "starter")
			w.compiler.EXPECT().ModuleCalls(gomock.Any(), w7dMainXML).Return(nil, nil, nil)
			w.compiler.EXPECT().BundleModules(w7dMainXML, []domain.ResolvedModule(nil)).Return(w7dBundledXML, nil)
			w.compiler.EXPECT().Validate(gomock.Any(), w7dBundledXML).Return(nil, nil)

			resp := do(w.router, r.WithContext(tc.context(t)))

			require.Equal(t, http.StatusServiceUnavailable, resp.Code, resp.Body.String())
			prob := decodeProblem(t, resp)
			assert.Equal(t, "UPSTREAM_UNAVAILABLE", prob["code"])
			assert.Equal(t, "an upstream dependency is temporarily unavailable", prob["detail"])
			assert.NotContains(t, resp.Body.String(), leaked)
		})
	}
}
