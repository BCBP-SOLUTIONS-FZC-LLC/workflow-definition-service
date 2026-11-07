package handler_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const addModuleVersionWiringProcessID = "Process_Review"

const addModuleVersionWiringCallingXML = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_Review" name="Review" isExecutable="false">
    <bpmn:startEvent id="Start_1"/>
    <bpmn:callActivity id="Call_Missing" name="Missing step">
      <bpmn:extensionElements>
        <zeebe:calledElement processId="Process_Missing" propagateAllChildVariables="false"/>
      </bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="End_1"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="Start_1" targetRef="Call_Missing"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Call_Missing" targetRef="End_1"/>
  </bpmn:process>
</bpmn:definitions>`

type addModuleVersionWiring struct {
	modules  *mocks.MockModuleRepository
	versions *mocks.MockModuleVersionRepository
	compiler *mocks.MockPlanCompiler
	router   *gin.Engine
}

func newAddModuleVersionWiring(t *testing.T, compiler port.PlanCompiler) *addModuleVersionWiring {
	t.Helper()
	ctrl := gomock.NewController(t)
	w := &addModuleVersionWiring{
		modules:  mocks.NewMockModuleRepository(ctrl),
		versions: mocks.NewMockModuleVersionRepository(ctrl),
	}
	if compiler == nil {
		w.compiler = mocks.NewMockPlanCompiler(ctrl)
		compiler = w.compiler
	}
	svc := service.NewModuleService(service.ModuleDeps{
		Transactor:       mocks.NewMockTransactor(ctrl),
		GlobalTransactor: mocks.NewMockTransactor(ctrl),
		Modules:          w.modules,
		GlobalModules:    mocks.NewMockModuleRepository(ctrl),
		Versions:         w.versions,
		GlobalVersions:   mocks.NewMockModuleVersionRepository(ctrl),
		Compiler:         compiler,
	})
	w.router = newRouter(handler.New(handler.Services{Modules: svc}))
	return w
}

func (w *addModuleVersionWiring) expectModule() {
	w.modules.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(&domain.Module{
		ID: testWFID, TenantID: &testTenantID, Scope: domain.ScopeTenant, ProcessID: addModuleVersionWiringProcessID,
	}, nil).AnyTimes()
}

func addModuleVersionWiringPath() string {
	return "/api/v1/modules/" + testWFID.String() + "/versions"
}

func addModuleVersionWiringPost(body any) *http.Request {
	return adminReq(http.MethodPost, addModuleVersionWiringPath(), body)
}

func addModuleVersionWiringRawPost(raw string) *http.Request {
	r := adminReq(http.MethodPost, addModuleVersionWiringPath(), nil)
	r.Body = io.NopCloser(strings.NewReader(raw))
	return r
}

func addModuleVersionWiringParams(t *testing.T, prob map[string]any) []map[string]any {
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

func addModuleVersionWiringCalls(callIDs ...string) []domain.ModuleCall {
	calls := make([]domain.ModuleCall, len(callIDs))
	for i, id := range callIDs {
		calls[i] = domain.ModuleCall{CallID: id, ProcessID: "Process_Missing"}
	}
	return calls
}

func TestAddModuleVersion_RejectedRequest_Returns400AndAddsNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		request    *http.Request
		wantDetail string
		wantParam  map[string]any
	}{
		{
			name:       "module id is not a UUID",
			request:    adminReq(http.MethodPost, "/api/v1/modules/not-a-uuid/versions", nil),
			wantDetail: "invalid id: not a valid UUID",
		},
		{
			name:       "data after the JSON value",
			request:    addModuleVersionWiringRawPost(`{"bpmn_xml":"<a/>"} {"bpmn_xml":"<b/>"}`),
			wantDetail: "request body is not valid JSON",
		},
		{
			name:       "bpmn_xml of the wrong JSON type",
			request:    addModuleVersionWiringPost(map[string]any{"bpmn_xml": 42}),
			wantDetail: "request body has a field of the wrong type",
			wantParam:  map[string]any{"name": "bpmn_xml", "reason": "has the wrong type"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			added := 0
			h := newModuleHandler(&fakeModuleSvc{
				get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
					return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
				},
				addVersion: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.AddModuleVersionReq) (*domain.ModuleVersion, error) {
					added++
					return &domain.ModuleVersion{ID: testVerID2}, nil
				},
			})

			w := do(newRouter(h), tc.request)

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, tc.wantDetail, prob["detail"])
			if tc.wantParam != nil {
				params := addModuleVersionWiringParams(t, prob)
				require.Len(t, params, 1)
				assert.Equal(t, tc.wantParam["name"], params[0]["name"])
				assert.Equal(t, tc.wantParam["reason"], params[0]["reason"])
			}
			assert.Zero(t, added, "a rejected request must not reach the service")
		})
	}
}

func TestAddModuleVersion_EmptyBpmnXml_IsRejectedAndNotReplacedByTheActiveVersion(t *testing.T) {
	t.Parallel()
	activeID := uuid.New()
	w := newAddModuleVersionWiring(t, bpmn_compiler.New())
	w.modules.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(&domain.Module{
		ID: testWFID, TenantID: &testTenantID, Scope: domain.ScopeTenant,
		ProcessID: addModuleVersionWiringProcessID, ActiveVersionID: &activeID,
	}, nil).AnyTimes()
	w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, activeID).
		Return(&domain.ModuleVersion{ID: activeID, ModuleID: testWFID, BPMNXML: addModuleVersionWiringCallingXML}, nil).AnyTimes()

	rec := do(w.router, addModuleVersionWiringPost(map[string]any{"bpmn_xml": ""}))

	require.GreaterOrEqual(t, rec.Code, http.StatusBadRequest, rec.Body.String())
	require.Less(t, rec.Code, http.StatusInternalServerError, rec.Body.String())
}

func TestAddModuleVersion_CallToAnUnresolvableModule_Returns422AndAddsNothing(t *testing.T) {
	t.Parallel()
	w := newAddModuleVersionWiring(t, bpmn_compiler.New())
	w.expectModule()
	w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Missing", int32(0)).
		Return(nil, domain.ErrNotFound)

	rec := do(w.router, addModuleVersionWiringPost(map[string]any{"bpmn_xml": addModuleVersionWiringCallingXML}))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	prob := decodeProblem(t, rec)
	assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
	params := addModuleVersionWiringParams(t, prob)
	require.Len(t, params, 1)
	assert.Equal(t, "Call_Missing", params[0]["name"])
	assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", params[0]["code"])
	assert.Contains(t, params[0]["reason"], "Process_Missing")
}

func TestAddModuleVersion_OnlyWarningsFound_Returns201AndStoresTheDraft(t *testing.T) {
	t.Parallel()
	w := newAddModuleVersionWiring(t, nil)
	w.expectModule()
	w.compiler.EXPECT().ProcessID(gomock.Any(), "<warned/>").Return(addModuleVersionWiringProcessID, nil)
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<warned/>").Return(nil, nil, nil)
	w.compiler.EXPECT().BundleModules("<warned/>", gomock.Any()).Return("<bundled/>", nil)
	w.compiler.EXPECT().ValidateModule(gomock.Any(), "<bundled/>", addModuleVersionWiringProcessID, false).
		Return([]domain.BPMNValidationError{
			{Code: "SOME_WARNING", NodeID: "Task_1", Message: "advisory only", Severity: domain.SeverityWarning},
		}, nil)
	var stored *domain.ModuleVersion
	w.versions.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, v *domain.ModuleVersion) error {
			stored = v
			return nil
		})

	rec := do(w.router, addModuleVersionWiringPost(map[string]any{"bpmn_xml": "<warned/>"}))

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.NotNil(t, stored, "the version is stored")
	assert.Equal(t, domain.VersionStatusDraft, stored.Status)
	assert.Equal(t, "<warned/>", stored.BPMNXML)
	assert.Equal(t, testWFID, stored.ModuleID)
	assert.Equal(t, domain.ScopeTenant, stored.Scope)
	assert.Equal(t, addModuleVersionWiringProcessID, stored.ProcessID)
	assert.True(t, stored.IsValid)
	require.NotNil(t, stored.TenantID)
	assert.Equal(t, testTenantID, *stored.TenantID)
	require.NotNil(t, stored.CreatedByUserID)
	assert.Equal(t, testUserID, *stored.CreatedByUserID)
	assert.Equal(t, stored.ID.String(), decodeProblem(t, rec)["version_id"])
}

func TestAddModuleVersion_ResolutionOrBundlingFails_ReturnsTheMatchingErrorAndAddsNothing(t *testing.T) {
	t.Parallel()
	const leaked = "pq: relation module_version is corrupt"
	unavailable := fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable)
	cases := []struct {
		name       string
		setup      func(w *addModuleVersionWiring)
		wantStatus int
		wantCode   string
	}{
		{
			name: "module lookup fails with an unclassified error",
			setup: func(w *addModuleVersionWiring) {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), gomock.Any()).Return(addModuleVersionWiringCalls("Call_1"), nil, nil)
				w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Missing", int32(0)).Return(nil, errors.New(leaked))
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
		},
		{
			name: "listing the module calls fails",
			setup: func(w *addModuleVersionWiring) {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), gomock.Any()).Return(nil, nil, errors.New(leaked))
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
		},
		{
			name: "bundling the modules fails",
			setup: func(w *addModuleVersionWiring) {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), gomock.Any()).Return(nil, nil, nil)
				w.compiler.EXPECT().BundleModules(gomock.Any(), gomock.Any()).Return("", errors.New(leaked))
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
		},
		{
			name: "module lookup fails because the database is unreachable",
			setup: func(w *addModuleVersionWiring) {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), gomock.Any()).Return(addModuleVersionWiringCalls("Call_1"), nil, nil)
				w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Missing", int32(0)).Return(nil, unavailable)
			},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newAddModuleVersionWiring(t, nil)
			w.expectModule()
			w.compiler.EXPECT().ProcessID(gomock.Any(), gomock.Any()).Return(addModuleVersionWiringProcessID, nil)
			tc.setup(w)

			rec := do(w.router, addModuleVersionWiringPost(map[string]any{"bpmn_xml": "<x/>"}))

			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			assert.Equal(t, tc.wantCode, decodeProblem(t, rec)["code"])
			assert.NotContains(t, rec.Body.String(), leaked, "the internal error text is not sent to the caller")
			assert.NotContains(t, rec.Body.String(), "connection refused")
		})
	}
}

func TestAddModuleVersion_IdempotencyKey_ReplaysTheFirstResponseOnlyWhenSent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		key          string
		wantAdded    int
		wantSameBody bool
	}{
		{name: "same key and body", key: "add-module-version-key", wantAdded: 1, wantSameBody: true},
		{name: "no key", key: "", wantAdded: 2, wantSameBody: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			added := 0
			svc := &fakeModuleSvc{
				get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
					return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
				},
				addVersion: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.AddModuleVersionReq) (*domain.ModuleVersion, error) {
					added++
					return &domain.ModuleVersion{ID: uuid.New(), Status: domain.VersionStatusDraft}, nil
				},
			}
			router := newRouter(handler.New(handler.Services{
				Modules: svc, Cache: newMemCache(), IdempotencyTTL: time.Hour,
			}))
			send := func() string {
				r := addModuleVersionWiringPost(map[string]any{"bpmn_xml": "<new/>"})
				if tc.key != "" {
					r.Header.Set("Idempotency-Key", tc.key)
				}
				rec := do(router, r)
				require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
				return rec.Body.String()
			}

			first := send()
			second := send()

			assert.Equal(t, tc.wantAdded, added)
			if tc.wantSameBody {
				assert.JSONEq(t, first, second, "the replay carries the first version id")
			} else {
				assert.NotEqual(t, first, second, "each call adds its own draft version")
			}
		})
	}
}
