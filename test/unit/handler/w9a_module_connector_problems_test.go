package handler_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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

const (
	w9aCredentialsPath = "/api/v1/connectors/credentials"
	w9aModulesPath     = "/api/v1/modules"
	w9aProblemBase     = "https://api.workflow.platform/errors/"
)

type w9aSecretsStore struct {
	mu      sync.Mutex
	written []string
	deleted []string
}

func (s *w9aSecretsStore) Write(_ context.Context, path string, _ map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.written = append(s.written, path)
	return nil
}

func (s *w9aSecretsStore) Delete(_ context.Context, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, path)
	return nil
}

func (s *w9aSecretsStore) writes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.written...)
}

func (s *w9aSecretsStore) deletes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.deleted...)
}

func w9aConnectorRouter(store port.SecretsClient) *gin.Engine {
	svc := service.NewConnectorService(service.ConnectorDeps{Secrets: store})
	return newRouter(handler.New(handler.Services{Connectors: svc}))
}

func w9aRequestAsTenant(tenantID uuid.UUID, method, path string, body any) *http.Request {
	r := adminReq(method, path, body)
	r.Header.Set("x-tenant-id", tenantID.String())
	return r
}

func w9aAssertProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, typeSuffix, code, instance string) map[string]any {
	t.Helper()
	require.Equal(t, status, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
	prob := decodeProblem(t, rec)
	assert.Equal(t, w9aProblemBase+typeSuffix, prob["type"])
	assert.Equal(t, code, prob["code"])
	assert.EqualValues(t, status, prob["status"])
	assert.Equal(t, instance, prob["instance"])
	return prob
}

func w9aInvalidParams(t *testing.T, prob map[string]any) []map[string]any {
	t.Helper()
	raw, ok := prob["invalid_params"].([]any)
	require.True(t, ok, "problem body carries invalid_params: %v", prob)
	out := make([]map[string]any, len(raw))
	for i, p := range raw {
		m, isMap := p.(map[string]any)
		require.True(t, isMap)
		out[i] = m
	}
	return out
}

func TestWriteConnectorCredential_SameTypeAndFieldFromTwoTenants_StoresEachUnderItsOwnTenantPrefix(t *testing.T) {
	t.Parallel()

	store := &w9aSecretsStore{}
	router := w9aConnectorRouter(store)
	tenantA, tenantB := uuid.New(), uuid.New()
	body := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "w9a-secret"}

	recA := do(router, w9aRequestAsTenant(tenantA, http.MethodPost, w9aCredentialsPath, body))
	recB := do(router, w9aRequestAsTenant(tenantB, http.MethodPost, w9aCredentialsPath, body))

	require.Equal(t, http.StatusCreated, recA.Code, recA.Body.String())
	require.Equal(t, http.StatusCreated, recB.Code, recB.Body.String())
	pathA, _ := decodeProblem(t, recA)["secret_path"].(string)
	pathB, _ := decodeProblem(t, recB)["secret_path"].(string)
	assert.NotEqual(t, pathA, pathB, "two tenants never share a secret path")
	assert.True(t, strings.HasPrefix(pathA, "connectors/"+tenantA.String()+"/"), "tenant A path: %s", pathA)
	assert.True(t, strings.HasPrefix(pathB, "connectors/"+tenantB.String()+"/"), "tenant B path: %s", pathB)
	assert.False(t, strings.Contains(pathA, tenantB.String()), "tenant A path does not name tenant B")
	assert.False(t, strings.Contains(pathB, tenantA.String()), "tenant B path does not name tenant A")
	assert.Equal(t, []string{pathA, pathB}, store.writes(), "each answer names the path that was written, in request order")
}

func TestWriteConnectorCredential_ConnectorTypeNotInTheRegistry_Returns400NamingTheType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		connectorType string
	}{
		{name: "unknown word", connectorType: "zzq_unknown"},
		{name: "registered type in upper case", connectorType: "SEND-EMAIL"},
		{name: "registered type with a capital letter", connectorType: "Send-Email"},
		{name: "registered type with a trailing space", connectorType: "send-email "},
		{name: "chat notification type is not a credential holder", connectorType: "chat-notify"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := &w9aSecretsStore{}
			body := map[string]any{"connector_type": tc.connectorType, "field_name": "apiKey", "value": "w9a-secret"}

			rec := do(w9aConnectorRouter(store), adminReq(http.MethodPost, w9aCredentialsPath, body))

			prob := w9aAssertProblem(t, rec, http.StatusBadRequest, "bad-request", "INVALID_INPUT", w9aCredentialsPath)
			assert.Contains(t, prob["detail"], fmt.Sprintf("unrecognized connector_type %q", tc.connectorType))
			assert.Empty(t, store.writes(), "an unregistered type never reaches the secrets store")
			assert.NotContains(t, rec.Body.String(), "w9a-secret")
		})
	}

	t.Run("the exact registered spelling is accepted", func(t *testing.T) {
		t.Parallel()
		store := &w9aSecretsStore{}
		body := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "w9a-secret"}

		rec := do(w9aConnectorRouter(store), adminReq(http.MethodPost, w9aCredentialsPath, body))

		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		assert.Len(t, store.writes(), 1)
	})
}

func TestWriteConnectorCredential_FieldNameOutsideTheAllowedCharacters_Returns400AndWritesNothing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		fieldName string
	}{
		{name: "space inside", fieldName: "api key"},
		{name: "dot inside", fieldName: "api.key"},
		{name: "dot dot", fieldName: ".."},
		{name: "non-ASCII letter", fieldName: "clé"},
		{name: "non-Latin letters", fieldName: "ключ"},
		{name: "non-ASCII digit", fieldName: "key٣"},
		{name: "trailing newline", fieldName: "key\n"},
		{name: "percent sign", fieldName: "key%2F"},
		{name: "colon", fieldName: "key:1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := &w9aSecretsStore{}
			body := map[string]any{"connector_type": "send-email", "field_name": tc.fieldName, "value": "w9a-secret"}

			rec := do(w9aConnectorRouter(store), adminReq(http.MethodPost, w9aCredentialsPath, body))

			prob := w9aAssertProblem(t, rec, http.StatusBadRequest, "bad-request", "INVALID_INPUT", w9aCredentialsPath)
			assert.Contains(t, prob["detail"], "field_name must match")
			assert.Empty(t, store.writes(), "a refused field name never reaches the secrets store")
			assert.NotContains(t, rec.Body.String(), "w9a-secret")
		})
	}
}

func TestRevokeConnectorCredential_FieldNameLength_Returns204AtTheCapAndProblemOneOver(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		length     int
		wantStatus int
	}{
		{name: "128 characters", length: 128, wantStatus: http.StatusNoContent},
		{name: "129 characters", length: 129, wantStatus: http.StatusBadRequest},
		{name: "1000 characters", length: 1000, wantStatus: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := &w9aSecretsStore{}
			fieldName := strings.Repeat("a", tc.length)
			path := w9aCredentialsPath + "/send-email/" + fieldName

			rec := do(w9aConnectorRouter(store), adminReq(http.MethodDelete, path, nil))

			if tc.wantStatus == http.StatusNoContent {
				require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
				assert.Empty(t, rec.Body.String())
				assert.Equal(t, []string{fmt.Sprintf("connectors/%s/send-email/%s", testTenantID, fieldName)}, store.deletes())
				return
			}
			prob := w9aAssertProblem(t, rec, http.StatusBadRequest, "bad-request", "INVALID_INPUT", path)
			assert.Contains(t, prob["detail"], "field_name must match")
			assert.Contains(t, prob["detail"], "at most 128 characters")
			assert.NotContains(t, prob["detail"], fieldName, "the oversized name is not echoed in the detail")
			assert.Empty(t, store.deletes(), "a refused field name never reaches the secrets store")
		})
	}
}

type w9aModuleWire struct {
	tx               *mocks.MockTransactor
	modules          *mocks.MockModuleRepository
	versions         *mocks.MockModuleVersionRepository
	workflowVersions *mocks.MockWorkflowVersionRepository
	compiler         *mocks.MockPlanCompiler
	log              *fakeLogger
	router           *gin.Engine
}

func newW9aModuleWire(t *testing.T, realCompiler bool) *w9aModuleWire {
	t.Helper()
	ctrl := gomock.NewController(t)
	w := &w9aModuleWire{
		tx:               mocks.NewMockTransactor(ctrl),
		modules:          mocks.NewMockModuleRepository(ctrl),
		versions:         mocks.NewMockModuleVersionRepository(ctrl),
		workflowVersions: mocks.NewMockWorkflowVersionRepository(ctrl),
		compiler:         mocks.NewMockPlanCompiler(ctrl),
		log:              &fakeLogger{},
	}
	var compiler port.PlanCompiler = w.compiler
	if realCompiler {
		compiler = bpmn_compiler.New()
	}
	svc := service.NewModuleService(service.ModuleDeps{
		Transactor:       w.tx,
		GlobalTransactor: mocks.NewMockTransactor(ctrl),
		Modules:          w.modules,
		GlobalModules:    mocks.NewMockModuleRepository(ctrl),
		Versions:         w.versions,
		GlobalVersions:   mocks.NewMockModuleVersionRepository(ctrl),
		WorkflowVersions: w.workflowVersions,
		Compiler:         compiler,
	})
	w.router = newRouter(handler.New(handler.Services{Modules: svc, Log: w.log}))
	return w
}

func (w *w9aModuleWire) expectTransactionRuns() {
	run := func(ctx context.Context, f func(context.Context) error) error { return f(ctx) }
	w.tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(run)
}

func (w *w9aModuleWire) expectValidModule(t *testing.T, bpmnXML string) {
	t.Helper()
	w.compiler.EXPECT().ProcessID(gomock.Any(), bpmnXML).Return("Process_Review", nil)
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), bpmnXML).Return(nil, nil, nil)
	w.compiler.EXPECT().BundleModules(bpmnXML, gomock.Any()).DoAndReturn(
		func(_ string, resolved []domain.ResolvedModule) (string, error) {
			assert.Empty(t, resolved, "a module without calls bundles nothing")
			return "<bundled/>", nil
		})
}

func (w *w9aModuleWire) createModule(bpmnXML string) *httptest.ResponseRecorder {
	return do(w.router, adminReq(http.MethodPost, w9aModulesPath, map[string]any{"name": "Review", "bpmn_xml": bpmnXML}))
}

func w9aPublishedModule(processID string, number int32, bpmnXML string) *domain.ModuleVersion {
	n := number
	return &domain.ModuleVersion{
		ID: uuid.New(), ModuleID: uuid.New(), ProcessID: processID, VersionNumber: &n,
		Status: domain.VersionStatusPublished, BPMNXML: bpmnXML,
	}
}

func (w *w9aModuleWire) resolveFromLibrary(t *testing.T, library map[string]*domain.ModuleVersion) {
	t.Helper()
	w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, gomock.Any(), int32(0)).DoAndReturn(
		func(_ context.Context, _ uuid.UUID, processID string, _ int32) (*domain.ModuleVersion, error) {
			if v, ok := library[processID]; ok {
				return v, nil
			}
			return nil, domain.ErrNotFound
		}).AnyTimes()
}

func TestListModules_RepositoryReportsUpstreamUnavailableWrapped_Returns503(t *testing.T) {
	t.Parallel()

	const leaked = "dial tcp 10.0.0.5:5432: connection refused"
	cases := []struct {
		name string
		err  error
	}{
		{name: "wrapped once", err: fmt.Errorf("%w: %s", domain.ErrUpstreamUnavailable, leaked)},
		{name: "wrapped twice", err: fmt.Errorf("query modules: %w", fmt.Errorf("%w: %s", domain.ErrUpstreamUnavailable, leaked))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newW9aModuleWire(t, false)
			w.modules.EXPECT().List(gomock.Any(), testTenantID, port.ModuleFilter{Page: 1, Limit: 20}).Return(nil, int64(0), tc.err)

			rec := do(w.router, req(http.MethodGet, w9aModulesPath, nil))

			prob := w9aAssertProblem(t, rec, http.StatusServiceUnavailable, "service-unavailable", "UPSTREAM_UNAVAILABLE", w9aModulesPath)
			assert.Equal(t, "an upstream dependency is temporarily unavailable", prob["detail"])
			assert.NotContains(t, rec.Body.String(), leaked, "the cause is not sent to the caller")
			assert.Equal(t, []string{"request failed"}, w.log.errorCalls, "the outage is logged once")
		})
	}
}

func TestCreateModule_CallNamesAModuleThatIsNotLiveForTheCaller_Returns422OnTheCall(t *testing.T) {
	t.Parallel()

	rootXML := moduleContractFailureCallerXML("Process_Root", "Root_CallGone", "Process_Gone")
	cases := []struct {
		name    string
		lookup  func(context.Context, uuid.UUID, string, int32) (*domain.ModuleVersion, error)
		wantMsg string
	}{
		{
			name: "no such module",
			lookup: func(context.Context, uuid.UUID, string, int32) (*domain.ModuleVersion, error) {
				return nil, domain.ErrNotFound
			},
		},
		{
			name: "module whose version has no number yet",
			lookup: func(_ context.Context, _ uuid.UUID, processID string, _ int32) (*domain.ModuleVersion, error) {
				return &domain.ModuleVersion{ID: uuid.New(), ProcessID: processID, Status: domain.VersionStatusDraft}, nil
			},
		},
		{
			name: "lookup answers neither a version nor an error",
			lookup: func(context.Context, uuid.UUID, string, int32) (*domain.ModuleVersion, error) {
				return nil, nil
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newW9aModuleWire(t, true)
			w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Gone", int32(0)).DoAndReturn(tc.lookup)

			rec := w.createModule(rootXML)

			prob := w9aAssertProblem(t, rec, http.StatusUnprocessableEntity, "validation-failed", "BPMN_VALIDATION_FAILED", w9aModulesPath)
			assert.Equal(t, "BPMN semantic or structural validation failed.", prob["detail"])
			params := w9aInvalidParams(t, prob)
			require.Len(t, params, 1)
			assert.Equal(t, "Root_CallGone", params[0]["name"])
			assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", params[0]["code"])
			assert.Contains(t, params[0]["reason"], `"Process_Gone"`)
			assert.Contains(t, params[0]["reason"], "published, unarchived module")
		})
	}
}

func TestCreateModule_CallToTheModulesOwnProcessAtLatest_Returns422WithoutAnyLookup(t *testing.T) {
	t.Parallel()

	w := newW9aModuleWire(t, false)
	w.compiler.EXPECT().ProcessID(gomock.Any(), "<bpmn/>").Return("Process_Self", nil)
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return([]domain.ModuleCall{
		{CallID: "Self_Call", ProcessID: "Process_Self"},
	}, nil, nil)

	rec := w.createModule("<bpmn/>")

	prob := w9aAssertProblem(t, rec, http.StatusUnprocessableEntity, "validation-failed", "BPMN_VALIDATION_FAILED", w9aModulesPath)
	params := w9aInvalidParams(t, prob)
	require.Len(t, params, 1)
	assert.Equal(t, "Self_Call", params[0]["name"])
	assert.Equal(t, "CALLED_ELEMENT_CYCLE", params[0]["code"])
	assert.Contains(t, params[0]["reason"], `"Process_Self"`)
	assert.Contains(t, params[0]["reason"], "the module being saved")
}

func TestCreateModule_ProcessThatCallsItselfInItsOwnDiagram_Returns422CalledElementCycle(t *testing.T) {
	t.Parallel()

	w := newW9aModuleWire(t, true)

	rec := w.createModule(moduleContractFailureCallerXML("Process_Self", "Self_Call", "Process_Self"))

	prob := w9aAssertProblem(t, rec, http.StatusUnprocessableEntity, "validation-failed", "BPMN_VALIDATION_FAILED", w9aModulesPath)
	params := w9aInvalidParams(t, prob)
	require.Len(t, params, 1)
	assert.Equal(t, "Self_Call", params[0]["name"])
	assert.Equal(t, "CALLED_ELEMENT_CYCLE", params[0]["code"])
}

func TestCreateModule_IssueInsideACalledModule_Returns422OnTheRootCallWithItsPath(t *testing.T) {
	t.Parallel()

	rootXML := moduleContractFailureCallerXML("Process_Root", "Root_CallMid", "Process_Mid")
	cases := []struct {
		name       string
		library    map[string]*domain.ModuleVersion
		wantPrefix string
	}{
		{
			name: "one module deep",
			library: map[string]*domain.ModuleVersion{
				"Process_Mid": w9aPublishedModule("Process_Mid", 2, moduleContractFailureCallerXML("Process_Mid", "Mid_CallGone", "Process_Gone")),
			},
			wantPrefix: `inside module "Process_Mid" version 2: `,
		},
		{
			name: "two modules deep",
			library: map[string]*domain.ModuleVersion{
				"Process_Mid":   w9aPublishedModule("Process_Mid", 1, moduleContractFailureCallerXML("Process_Mid", "Mid_CallInner", "Process_Inner")),
				"Process_Inner": w9aPublishedModule("Process_Inner", 4, moduleContractFailureCallerXML("Process_Inner", "Inner_CallGone", "Process_Gone")),
			},
			wantPrefix: `inside module "Process_Mid" version 1 → module "Process_Inner" version 4: `,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newW9aModuleWire(t, true)
			w.resolveFromLibrary(t, tc.library)

			rec := w.createModule(rootXML)

			prob := w9aAssertProblem(t, rec, http.StatusUnprocessableEntity, "validation-failed", "BPMN_VALIDATION_FAILED", w9aModulesPath)
			params := w9aInvalidParams(t, prob)
			require.Len(t, params, 1)
			assert.Equal(t, "Root_CallMid", params[0]["name"], "the issue is attributed to the call in the module being saved")
			assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", params[0]["code"])
			reason, _ := params[0]["reason"].(string)
			assert.True(t, strings.HasPrefix(reason, tc.wantPrefix), "reason %q starts with %q", reason, tc.wantPrefix)
			assert.Contains(t, reason, `"Process_Gone"`)
		})
	}
}

func TestCreateModule_WriteStepFailsWithAGenericError_Returns500AndLogsIt(t *testing.T) {
	t.Parallel()

	const leaked = "pq: deadlock detected while locking relation module"
	cases := []struct {
		name  string
		setup func(t *testing.T, w *w9aModuleWire)
	}{
		{
			name: "process id check",
			setup: func(_ *testing.T, w *w9aModuleWire) {
				w.modules.EXPECT().ProcessIDInUse(gomock.Any(), &testTenantID, "Process_Review").Return(false, errors.New(leaked))
			},
		},
		{
			name: "module insert",
			setup: func(t *testing.T, w *w9aModuleWire) {
				w.modules.EXPECT().ProcessIDInUse(gomock.Any(), &testTenantID, "Process_Review").Return(false, nil)
				w.modules.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, m *domain.Module) error {
						assert.Equal(t, "Process_Review", m.ProcessID)
						require.NotNil(t, m.TenantID)
						assert.Equal(t, testTenantID, *m.TenantID)
						require.NotNil(t, m.CreatedByUserID)
						assert.Equal(t, testUserID, *m.CreatedByUserID)
						return errors.New(leaked)
					})
			},
		},
		{
			name: "version insert",
			setup: func(t *testing.T, w *w9aModuleWire) {
				w.modules.EXPECT().ProcessIDInUse(gomock.Any(), &testTenantID, "Process_Review").Return(false, nil)
				w.modules.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
				w.versions.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, v *domain.ModuleVersion) error {
						assert.Equal(t, "Process_Review", v.ProcessID)
						require.NotNil(t, v.TenantID)
						assert.Equal(t, testTenantID, *v.TenantID)
						require.NotNil(t, v.CreatedByUserID)
						assert.Equal(t, testUserID, *v.CreatedByUserID)
						return errors.New(leaked)
					})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newW9aModuleWire(t, false)
			w.expectTransactionRuns()
			w.expectValidModule(t, "<bpmn/>")
			w.compiler.EXPECT().ValidateModule(gomock.Any(), "<bundled/>", "Process_Review", false).Return(nil, nil)
			tc.setup(t, w)

			rec := w.createModule("<bpmn/>")

			prob := w9aAssertProblem(t, rec, http.StatusInternalServerError, "internal-error", "INTERNAL_ERROR", w9aModulesPath)
			assert.Equal(t, "an unexpected error occurred", prob["detail"])
			assert.NotContains(t, rec.Body.String(), leaked)
			assert.Equal(t, []string{"request failed"}, w.log.errorCalls)
		})
	}
}

func TestCreateModule_CompilerOrLookupFailsUnexpectedly_Returns500UnlessTheModuleIsMissing(t *testing.T) {
	t.Parallel()

	const leaked = "pq: could not serialize access"
	rootXML := moduleContractFailureCallerXML("Process_Root", "Root_CallOther", "Process_Other")
	cases := []struct {
		name       string
		realCompil bool
		xml        string
		setup      func(t *testing.T, w *w9aModuleWire)
		wantStatus int
		wantCode   string
	}{
		{
			name: "module validation fails in the compiler",
			xml:  "<bpmn/>",
			setup: func(t *testing.T, w *w9aModuleWire) {
				w.expectValidModule(t, "<bpmn/>")
				w.compiler.EXPECT().ValidateModule(gomock.Any(), "<bundled/>", "Process_Review", false).Return(nil, errors.New(leaked))
			},
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_ERROR",
		},
		{
			name:       "module version lookup fails with an untyped error",
			realCompil: true,
			xml:        rootXML,
			setup: func(_ *testing.T, w *w9aModuleWire) {
				w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Other", int32(0)).Return(nil, errors.New(leaked))
			},
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_ERROR",
		},
		{
			name:       "module version lookup answers not found",
			realCompil: true,
			xml:        rootXML,
			setup: func(_ *testing.T, w *w9aModuleWire) {
				w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Other", int32(0)).Return(nil, domain.ErrNotFound)
			},
			wantStatus: http.StatusUnprocessableEntity, wantCode: "BPMN_VALIDATION_FAILED",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newW9aModuleWire(t, tc.realCompil)
			tc.setup(t, w)

			rec := w.createModule(tc.xml)

			prob := decodeProblem(t, rec)
			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			assert.Equal(t, tc.wantCode, prob["code"])
			assert.Equal(t, w9aModulesPath, prob["instance"])
			assert.NotContains(t, rec.Body.String(), leaked)
			if tc.wantStatus == http.StatusInternalServerError {
				assert.Equal(t, w9aProblemBase+"internal-error", prob["type"])
				assert.Equal(t, "an unexpected error occurred", prob["detail"])
				assert.Equal(t, []string{"request failed"}, w.log.errorCalls)
			} else {
				assert.Empty(t, w.log.errorCalls, "a refused module is not a server failure")
			}
		})
	}
}

func TestCreateModule_BPMNThatCannotBeParsed_Returns400AndWarnsOnlyForForbiddenXML(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		bpmnXML   string
		wantWarns []string
	}{
		{name: "mismatched closing tag", bpmnXML: `<zzq_a><zzq_b></zzq_a>`},
		{name: "document cut off inside an element", bpmnXML: `<definitions><process id="Process_Cut"`},
		{
			name:      "document type declaration with an entity",
			bpmnXML:   `<?xml version="1.0"?><!DOCTYPE zzq [<!ENTITY zzqent "x">]><definitions/>`,
			wantWarns: []string{"forbidden XML construct rejected"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newW9aModuleWire(t, true)

			rec := w.createModule(tc.bpmnXML)

			prob := w9aAssertProblem(t, rec, http.StatusBadRequest, "bad-request", "INVALID_BPMN_XML", w9aModulesPath)
			assert.Equal(t, "the BPMN document could not be parsed", prob["detail"])
			assert.NotContains(t, rec.Body.String(), "zzqent", "parser detail is not sent to the caller")
			assert.Equal(t, tc.wantWarns, w.log.warnCalls)
			assert.Empty(t, w.log.errorCalls, "a malformed document is a client error, not a server failure")
		})
	}
}

func TestListModuleUsages_ModuleDoesNotExist_Returns404WithoutListingUsages(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{name: "repository answers not found", err: domain.ErrNotFound},
		{name: "repository answers not found wrapped", err: fmt.Errorf("module lookup: %w", domain.ErrNotFound)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newW9aModuleWire(t, false)
			moduleID := uuid.New()
			path := "/api/v1/modules/" + moduleID.String() + "/usages"
			w.modules.EXPECT().GetByID(gomock.Any(), testTenantID, moduleID).Return(nil, tc.err)

			rec := do(w.router, req(http.MethodGet, path, nil))

			prob := w9aAssertProblem(t, rec, http.StatusNotFound, "not-found", "NOT_FOUND", path)
			assert.Equal(t, "resource not found", prob["detail"])
			assert.Empty(t, w.log.errorCalls, "a missing module is not a server failure")
		})
	}
}

func TestGetModuleContract_CallNamesAModuleWithoutAPublishedVersion_Returns422OnTheCallNode(t *testing.T) {
	t.Parallel()

	rootXML := moduleContractFailureCallerXML("Process_Root", "Root_CallGone", "Process_Gone")
	cases := []struct {
		name   string
		lookup func(context.Context, uuid.UUID, string, int32) (*domain.ModuleVersion, error)
	}{
		{
			name: "no such module",
			lookup: func(context.Context, uuid.UUID, string, int32) (*domain.ModuleVersion, error) {
				return nil, domain.ErrNotFound
			},
		},
		{
			name: "module has only an unpublished version",
			lookup: func(_ context.Context, _ uuid.UUID, processID string, _ int32) (*domain.ModuleVersion, error) {
				return &domain.ModuleVersion{ID: uuid.New(), ProcessID: processID, Status: domain.VersionStatusDraft}, nil
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newModuleContractFailureWire(t, bpmn_compiler.New())
			w.storedVersion("Process_Root", rootXML)
			w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Gone", int32(0)).DoAndReturn(tc.lookup)
			path := "/api/v1/modules/" + testWFID.String() + "/versions/" + testVerID.String() + "/contract"

			rec := do(w.router, req(http.MethodGet, path, nil))

			prob := w9aAssertProblem(t, rec, http.StatusUnprocessableEntity, "validation-failed", "BPMN_VALIDATION_FAILED", path)
			params := w9aInvalidParams(t, prob)
			require.Len(t, params, 1)
			assert.Equal(t, "Root_CallGone", params[0]["name"])
			assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", params[0]["code"])
			assert.Contains(t, params[0]["reason"], `"Process_Gone"`)
		})
	}
}

func TestGetModuleContract_IssueInsideACalledModule_Returns422OnTheRootCallWithInsideModuleReason(t *testing.T) {
	t.Parallel()

	w := newModuleContractFailureWire(t, bpmn_compiler.New())
	w.storedVersion("Process_Root", moduleContractFailureCallerXML("Process_Root", "Root_CallInner", "Process_Inner"))
	inner := w9aPublishedModule("Process_Inner", 2, moduleContractFailureCallerXML("Process_Inner", "Inner_CallGone", "Process_Gone"))
	w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Inner", int32(0)).Return(inner, nil)
	w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Gone", int32(0)).Return(nil, domain.ErrNotFound)
	path := "/api/v1/modules/" + testWFID.String() + "/versions/" + testVerID.String() + "/contract"

	rec := do(w.router, req(http.MethodGet, path, nil))

	prob := w9aAssertProblem(t, rec, http.StatusUnprocessableEntity, "validation-failed", "BPMN_VALIDATION_FAILED", path)
	params := w9aInvalidParams(t, prob)
	require.Len(t, params, 1)
	assert.Equal(t, "Root_CallInner", params[0]["name"], "the issue is attributed to the root call, not to the call inside the module")
	assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", params[0]["code"])
	reason, _ := params[0]["reason"].(string)
	assert.True(t, strings.HasPrefix(reason, `inside module "Process_Inner" version 2: `), "reason: %s", reason)
	assert.Contains(t, reason, `"Process_Gone"`)
}
