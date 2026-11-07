package handler_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type createModuleWiring struct {
	tx             *mocks.MockTransactor
	globalTx       *mocks.MockTransactor
	modules        *mocks.MockModuleRepository
	globalModules  *mocks.MockModuleRepository
	versions       *mocks.MockModuleVersionRepository
	globalVersions *mocks.MockModuleVersionRepository
	compiler       *mocks.MockPlanCompiler
	router         *gin.Engine
}

func newCreateModuleWiring(t *testing.T) *createModuleWiring {
	t.Helper()
	ctrl := gomock.NewController(t)
	w := &createModuleWiring{
		tx:             mocks.NewMockTransactor(ctrl),
		globalTx:       mocks.NewMockTransactor(ctrl),
		modules:        mocks.NewMockModuleRepository(ctrl),
		globalModules:  mocks.NewMockModuleRepository(ctrl),
		versions:       mocks.NewMockModuleVersionRepository(ctrl),
		globalVersions: mocks.NewMockModuleVersionRepository(ctrl),
		compiler:       mocks.NewMockPlanCompiler(ctrl),
	}
	svc := service.NewModuleService(service.ModuleDeps{
		Transactor:       w.tx,
		GlobalTransactor: w.globalTx,
		Modules:          w.modules,
		GlobalModules:    w.globalModules,
		Versions:         w.versions,
		GlobalVersions:   w.globalVersions,
		Compiler:         w.compiler,
	})
	w.router = newRouter(handler.New(handler.Services{Modules: svc}))
	return w
}

func createModuleWiringPassthrough(tx *mocks.MockTransactor) {
	run := func(ctx context.Context, f func(context.Context) error) error { return f(ctx) }
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(run)
}

func createModuleWiringBody(scope string) map[string]any {
	body := map[string]any{"name": "Review", "bpmn_xml": "<bpmn/>"}
	if scope != "" {
		body["scope"] = scope
	}
	return body
}

func createModuleWiringCalls(callIDs ...string) []domain.ModuleCall {
	calls := make([]domain.ModuleCall, len(callIDs))
	for i, id := range callIDs {
		calls[i] = domain.ModuleCall{CallID: id, ProcessID: "Process_Missing"}
	}
	return calls
}

func createModuleWiringInvalidParams(t *testing.T, prob map[string]any) []map[string]any {
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

func TestCreateModule_FieldLength_Boundaries(t *testing.T) {
	cases := []struct {
		name        string
		moduleName  string
		description string
		wantParams  []map[string]any
	}{
		{
			name:       "name one over the limit",
			moduleName: strings.Repeat("n", 256),
			wantParams: []map[string]any{
				{"name": "name", "reason": "must not exceed 255 characters"},
			},
		},
		{
			name:        "description one over the limit",
			moduleName:  "Review",
			description: strings.Repeat("d", 2001),
			wantParams: []map[string]any{
				{"name": "description", "reason": "must not exceed 2000 characters"},
			},
		},
		{
			name:        "both over the limit are listed together",
			moduleName:  strings.Repeat("n", 256),
			description: strings.Repeat("d", 2001),
			wantParams: []map[string]any{
				{"name": "name", "reason": "must not exceed 255 characters"},
				{"name": "description", "reason": "must not exceed 2000 characters"},
			},
		},
		{
			name:        "name of 255 and description of 2000 are accepted unchanged",
			moduleName:  strings.Repeat("n", 255),
			description: strings.Repeat("d", 2000),
		},
		{
			name:        "multibyte runes count once each",
			moduleName:  strings.Repeat("é", 255),
			description: strings.Repeat("é", 2000),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []service.CreateModuleReq
			h := newModuleHandler(&fakeModuleSvc{
				create: func(_ context.Context, _, _ uuid.UUID, r service.CreateModuleReq) (*domain.Module, *domain.ModuleVersion, error) {
					got = append(got, r)
					return &domain.Module{ID: testWFID}, &domain.ModuleVersion{ID: testVerID, Status: domain.VersionStatusDraft}, nil
				},
			})
			body := map[string]any{"name": tc.moduleName, "description": tc.description, "bpmn_xml": "<bpmn/>"}

			w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/modules", body))

			if tc.wantParams == nil {
				require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
				require.Len(t, got, 1)
				assert.Equal(t, tc.moduleName, got[0].Name)
				assert.Equal(t, tc.description, got[0].Description)
				return
			}
			require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "INVALID_INPUT", prob["code"])
			assert.Equal(t, "request fields exceed maximum length", prob["detail"])
			params := createModuleWiringInvalidParams(t, prob)
			require.Len(t, params, len(tc.wantParams))
			for i, want := range tc.wantParams {
				assert.Equal(t, want["name"], params[i]["name"])
				assert.Equal(t, want["reason"], params[i]["reason"])
			}
			assert.Empty(t, got, "an over-long field is rejected before the service runs")
		})
	}
}

func TestCreateModule_SameIdempotencyKey_ReplaysTheFirstCreation(t *testing.T) {
	cache := newMemCache()
	created := 0
	svc := &fakeModuleSvc{
		create: func(context.Context, uuid.UUID, uuid.UUID, service.CreateModuleReq) (*domain.Module, *domain.ModuleVersion, error) {
			created++
			return &domain.Module{ID: uuid.New()}, &domain.ModuleVersion{ID: uuid.New(), Status: domain.VersionStatusDraft}, nil
		},
	}
	router := newRouter(handler.New(handler.Services{
		Modules: svc, Cache: cache, IdempotencyTTL: time.Hour,
	}))
	send := func() *httptest.ResponseRecorder {
		r := adminReq(http.MethodPost, "/api/v1/modules", createModuleWiringBody(""))
		r.Header.Set("Idempotency-Key", "create-module-key")
		return do(router, r)
	}

	first := send()
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	second := send()

	assert.Equal(t, http.StatusCreated, second.Code, second.Body.String())
	assert.JSONEq(t, first.Body.String(), second.Body.String(), "the replay carries the first module and version ids")
	assert.Equal(t, 1, created, "the module is created once")
}

func TestCreateModule_GlobalCatalog_ProcessIDTaken_Returns409(t *testing.T) {
	w := newCreateModuleWiring(t)
	createModuleWiringPassthrough(w.globalTx)
	w.compiler.EXPECT().ProcessID(gomock.Any(), "<bpmn/>").Return("Process_Review", nil)
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	w.compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bundled/>", nil)
	w.compiler.EXPECT().ValidateModule(gomock.Any(), "<bundled/>", "Process_Review", true).Return(nil, nil)
	w.globalModules.EXPECT().ProcessIDInUse(gomock.Any(), (*uuid.UUID)(nil), "Process_Review").Return(true, nil)

	rec := do(w.router, platformOperatorReq(http.MethodPost, "/api/v1/modules", createModuleWiringBody("global")))

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	prob := decodeProblem(t, rec)
	assert.Equal(t, "MODULE_PROCESS_ID_CONFLICT", prob["code"])
	assert.Contains(t, prob["detail"], "Process_Review")
}

func TestCreateModule_ProcessIDTakenInTheOtherCatalog_IsNotAConflict(t *testing.T) {
	cases := []struct {
		name  string
		scope string
		setup func(t *testing.T, w *createModuleWiring, tenantID uuid.UUID)
	}{
		{
			name:  "tenant module checks only the tenant catalog",
			scope: "",
			setup: func(t *testing.T, w *createModuleWiring, tenantID uuid.UUID) {
				var created uuid.UUID
				createModuleWiringPassthrough(w.tx)
				w.modules.EXPECT().ProcessIDInUse(gomock.Any(), &tenantID, "Process_Review").Return(false, nil)
				w.modules.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, m *domain.Module) error {
						created = m.ID
						assert.Equal(t, domain.ScopeTenant, m.Scope)
						require.NotNil(t, m.TenantID)
						assert.Equal(t, tenantID, *m.TenantID)
						assert.Equal(t, "Process_Review", m.ProcessID)
						return nil
					})
				w.versions.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, v *domain.ModuleVersion) error {
						assert.Equal(t, created, v.ModuleID)
						return nil
					})
			},
		},
		{
			name:  "global module checks only the global catalog",
			scope: "global",
			setup: func(t *testing.T, w *createModuleWiring, _ uuid.UUID) {
				var created uuid.UUID
				createModuleWiringPassthrough(w.globalTx)
				w.globalModules.EXPECT().ProcessIDInUse(gomock.Any(), (*uuid.UUID)(nil), "Process_Review").Return(false, nil)
				w.globalModules.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, m *domain.Module) error {
						created = m.ID
						assert.Equal(t, domain.ScopeGlobal, m.Scope)
						assert.Nil(t, m.TenantID)
						assert.Equal(t, "Process_Review", m.ProcessID)
						return nil
					})
				w.globalVersions.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, v *domain.ModuleVersion) error {
						assert.Equal(t, created, v.ModuleID)
						return nil
					})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newCreateModuleWiring(t)
			w.compiler.EXPECT().ProcessID(gomock.Any(), gomock.Any()).Return("Process_Review", nil)
			w.compiler.EXPECT().ModuleCalls(gomock.Any(), gomock.Any()).Return(nil, nil, nil)
			w.compiler.EXPECT().BundleModules(gomock.Any(), gomock.Any()).Return("<bundled/>", nil)
			w.compiler.EXPECT().ValidateModule(gomock.Any(), gomock.Any(), "Process_Review", tc.scope == "global").Return(nil, nil)
			tc.setup(t, w, testTenantID)

			rec := do(w.router, platformOperatorReq(http.MethodPost, "/api/v1/modules", createModuleWiringBody(tc.scope)))

			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "Module created")
		})
	}
}

func TestCreateModule_BundlingFailure_Returns500AndWritesNothing(t *testing.T) {
	const leaked = "pq: relation module_version is corrupt"
	cases := []struct {
		name  string
		setup func(w *createModuleWiring)
	}{
		{
			name: "module lookup fails",
			setup: func(w *createModuleWiring) {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), gomock.Any()).Return(createModuleWiringCalls("Call_1"), nil, nil)
				w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Missing", int32(0)).
					Return(nil, errors.New(leaked))
			},
		},
		{
			name: "listing the module calls fails",
			setup: func(w *createModuleWiring) {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), gomock.Any()).Return(nil, nil, errors.New(leaked))
			},
		},
		{
			name: "bundling the modules fails",
			setup: func(w *createModuleWiring) {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), gomock.Any()).Return(nil, nil, nil)
				w.compiler.EXPECT().BundleModules(gomock.Any(), gomock.Any()).Return("", errors.New(leaked))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newCreateModuleWiring(t)
			w.compiler.EXPECT().ProcessID(gomock.Any(), gomock.Any()).Return("Process_Review", nil)
			tc.setup(w)

			rec := do(w.router, adminReq(http.MethodPost, "/api/v1/modules", createModuleWiringBody("")))

			require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
			prob := decodeProblem(t, rec)
			assert.Equal(t, "INTERNAL_ERROR", prob["code"])
			assert.NotContains(t, rec.Body.String(), leaked, "the internal error text is not sent to the caller")
		})
	}
}

func TestCreateModule_DatabaseUnreachable_Returns503(t *testing.T) {
	unavailable := fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable)
	cases := []struct {
		name  string
		setup func(w *createModuleWiring)
	}{
		{
			name: "during the process id check",
			setup: func(w *createModuleWiring) {
				w.modules.EXPECT().ProcessIDInUse(gomock.Any(), gomock.Any(), "Process_Review").Return(false, unavailable)
			},
		},
		{
			name: "during the module insert",
			setup: func(w *createModuleWiring) {
				w.modules.EXPECT().ProcessIDInUse(gomock.Any(), gomock.Any(), "Process_Review").Return(false, nil)
				w.modules.EXPECT().Create(gomock.Any(), gomock.Any()).Return(unavailable)
			},
		},
		{
			name: "during the version insert",
			setup: func(w *createModuleWiring) {
				w.modules.EXPECT().ProcessIDInUse(gomock.Any(), gomock.Any(), "Process_Review").Return(false, nil)
				w.modules.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
				w.versions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(unavailable)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newCreateModuleWiring(t)
			createModuleWiringPassthrough(w.tx)
			w.compiler.EXPECT().ProcessID(gomock.Any(), gomock.Any()).Return("Process_Review", nil)
			w.compiler.EXPECT().ModuleCalls(gomock.Any(), gomock.Any()).Return(nil, nil, nil)
			w.compiler.EXPECT().BundleModules(gomock.Any(), gomock.Any()).Return("<bundled/>", nil)
			w.compiler.EXPECT().ValidateModule(gomock.Any(), gomock.Any(), "Process_Review", false).Return(nil, nil)
			tc.setup(w)

			rec := do(w.router, adminReq(http.MethodPost, "/api/v1/modules", createModuleWiringBody("")))

			require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
			prob := decodeProblem(t, rec)
			assert.Equal(t, "UPSTREAM_UNAVAILABLE", prob["code"])
			assert.Equal(t, "an upstream dependency is temporarily unavailable", prob["detail"])
		})
	}
}

func TestCreateModule_UnresolvedCall_Returns422AndWritesNothing(t *testing.T) {
	t.Run("global module calling a module that exists only for a tenant", func(t *testing.T) {
		w := newCreateModuleWiring(t)
		w.compiler.EXPECT().ProcessID(gomock.Any(), gomock.Any()).Return("Process_Review", nil)
		w.compiler.EXPECT().ModuleCalls(gomock.Any(), gomock.Any()).Return(createModuleWiringCalls("Call_Tenant"), nil, nil)
		w.globalVersions.EXPECT().Resolve(gomock.Any(), uuid.Nil, "Process_Missing", int32(0)).Return(nil, domain.ErrNotFound)

		rec := do(w.router, platformOperatorReq(http.MethodPost, "/api/v1/modules", createModuleWiringBody("global")))

		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		prob := decodeProblem(t, rec)
		assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
		params := createModuleWiringInvalidParams(t, prob)
		require.Len(t, params, 1)
		assert.Equal(t, "Call_Tenant", params[0]["name"])
		assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", params[0]["code"])
		assert.Contains(t, params[0]["reason"], "Process_Missing")
	})

	t.Run("two calls of one missing module are reported once each with one lookup", func(t *testing.T) {
		w := newCreateModuleWiring(t)
		w.compiler.EXPECT().ProcessID(gomock.Any(), gomock.Any()).Return("Process_Review", nil)
		w.compiler.EXPECT().ModuleCalls(gomock.Any(), gomock.Any()).Return(createModuleWiringCalls("Call_A", "Call_B"), nil, nil)
		w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Missing", int32(0)).
			Return(nil, domain.ErrNotFound).Times(1)

		rec := do(w.router, adminReq(http.MethodPost, "/api/v1/modules", createModuleWiringBody("")))

		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		prob := decodeProblem(t, rec)
		assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
		params := createModuleWiringInvalidParams(t, prob)
		require.Len(t, params, 2)
		assert.Equal(t, "Call_A", params[0]["name"])
		assert.Equal(t, "Call_B", params[1]["name"])
		for _, p := range params {
			assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", p["code"])
		}
	})
}

func TestCreateModule_CallIssuesFromTheCompiler_Returns422WithThoseIssues(t *testing.T) {
	w := newCreateModuleWiring(t)
	w.compiler.EXPECT().ProcessID(gomock.Any(), gomock.Any()).Return("Process_Review", nil)
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), gomock.Any()).Return(nil, []domain.BPMNValidationError{
		{Code: domain.BPMNErrorCode("CALL_ISSUE_ONE"), NodeID: "Call_1", Message: "first call is malformed", Severity: domain.SeverityError},
		{Code: domain.BPMNErrorCode("CALL_ISSUE_TWO"), NodeID: "Call_2", Message: "second call is malformed", Severity: domain.SeverityError},
	}, nil)

	rec := do(w.router, adminReq(http.MethodPost, "/api/v1/modules", createModuleWiringBody("")))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	prob := decodeProblem(t, rec)
	assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
	params := createModuleWiringInvalidParams(t, prob)
	require.Len(t, params, 2)
	assert.Equal(t, map[string]any{"name": "Call_1", "reason": "first call is malformed", "code": "CALL_ISSUE_ONE"}, params[0])
	assert.Equal(t, map[string]any{"name": "Call_2", "reason": "second call is malformed", "code": "CALL_ISSUE_TWO"}, params[1])
}
