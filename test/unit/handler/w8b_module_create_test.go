package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
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

type w8bInfoLogger struct {
	mu    sync.Mutex
	infos []w8bLogEntry
}

func (l *w8bInfoLogger) Debug(string, map[string]any) { /* no-op */ }
func (l *w8bInfoLogger) Warn(string, map[string]any)  { /* no-op */ }
func (l *w8bInfoLogger) Error(string, map[string]any) { /* no-op */ }
func (l *w8bInfoLogger) Fatal(string, map[string]any) { /* no-op */ }
func (l *w8bInfoLogger) Info(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infos = append(l.infos, w8bLogEntry{msg: msg, fields: fields})
}

func (l *w8bInfoLogger) recordedInfos() []w8bLogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]w8bLogEntry(nil), l.infos...)
}

func TestCreateModuleW8b_GlobalScopeByANonOperator_Returns403WithoutParsingTheXML(t *testing.T) {
	t.Parallel()

	const garbledXML = "<definitions <<< not xml at all"
	cases := []struct {
		name string
		body map[string]any
	}{
		{"unparseable bpmn_xml", map[string]any{"name": "Review", "bpmn_xml": garbledXML, "scope": "global"}},
		{"valid-looking bpmn_xml", map[string]any{"name": "Review", "bpmn_xml": "<bpmn/>", "scope": "global"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			compiler := mocks.NewMockPlanCompiler(ctrl)
			tx := mocks.NewMockTransactor(ctrl)
			globalTx := mocks.NewMockTransactor(ctrl)
			modules := mocks.NewMockModuleRepository(ctrl)
			globalModules := mocks.NewMockModuleRepository(ctrl)
			versions := mocks.NewMockModuleVersionRepository(ctrl)
			globalVersions := mocks.NewMockModuleVersionRepository(ctrl)
			svc := service.NewModuleService(service.ModuleDeps{
				Transactor: tx, GlobalTransactor: globalTx,
				Modules: modules, GlobalModules: globalModules,
				Versions: versions, GlobalVersions: globalVersions,
				Compiler: compiler,
			})
			router := newRouter(handler.New(handler.Services{Modules: svc}))

			w := do(router, adminReq(http.MethodPost, w8bModulesPath, tc.body))

			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			assert.Contains(t, w.Header().Get("Content-Type"), "application/problem+json")
			prob := decodeProblem(t, w)
			assert.Equal(t, "https://api.workflow.platform/errors/forbidden", prob["type"])
			assert.Equal(t, "FORBIDDEN", prob["code"])
			assert.EqualValues(t, http.StatusForbidden, prob["status"])
			assert.Equal(t, w8bModulesPath, prob["instance"])
			assert.Equal(t, "caller lacks platform_operator role", prob["detail"])
			assert.NotContains(t, w.Body.String(), "BPMN_VALIDATION_FAILED")
		})
	}
}

func TestCreateModuleW8b_OmittedOrExplicitTenantScope_StoresATenantModuleAndAnswers201(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body map[string]any
	}{
		{"scope omitted", map[string]any{"name": "Review", "description": "d", "bpmn_xml": "<bpmn/>"}},
		{"scope tenant sent explicitly", map[string]any{"name": "Review", "description": "d", "bpmn_xml": "<bpmn/>", "scope": "tenant"}},
		{"scope empty string", map[string]any{"name": "Review", "description": "d", "bpmn_xml": "<bpmn/>", "scope": ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			compiler := mocks.NewMockPlanCompiler(ctrl)
			tx := mocks.NewMockTransactor(ctrl)
			modules := mocks.NewMockModuleRepository(ctrl)
			versions := mocks.NewMockModuleVersionRepository(ctrl)
			log := &w8bInfoLogger{}
			svc := service.NewModuleService(service.ModuleDeps{
				Transactor: tx,
				Modules:    modules,
				Versions:   versions,
				Compiler:   compiler,
				Log:        log,
			})
			router := newRouter(handler.New(handler.Services{Modules: svc}))

			var storedModule *domain.Module
			var storedVersion *domain.ModuleVersion
			compiler.EXPECT().ProcessID(gomock.Any(), "<bpmn/>").Return("Process_Review", nil)
			compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
			compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bundled/>", nil)
			compiler.EXPECT().ValidateModule(gomock.Any(), "<bundled/>", "Process_Review", false).Return(nil, nil)
			tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, f func(context.Context) error) error { return f(ctx) })
			modules.EXPECT().ProcessIDInUse(gomock.Any(), &testTenantID, "Process_Review").Return(false, nil)
			modules.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, m *domain.Module) error {
					storedModule = m
					return nil
				})
			versions.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, v *domain.ModuleVersion) error {
					storedVersion = v
					return nil
				})

			w := do(router, adminReq(http.MethodPost, w8bModulesPath, tc.body))

			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			require.NotNil(t, storedModule)
			require.NotNil(t, storedVersion)
			assert.Equal(t, domain.ScopeTenant, storedModule.Scope)
			require.NotNil(t, storedModule.TenantID)
			assert.Equal(t, testTenantID, *storedModule.TenantID)
			require.NotNil(t, storedModule.CreatedByUserID)
			assert.Equal(t, testUserID, *storedModule.CreatedByUserID)
			assert.Equal(t, "Review", storedModule.Name)
			assert.Equal(t, "d", storedModule.Description)
			assert.Equal(t, "Process_Review", storedModule.ProcessID)
			assert.Equal(t, domain.ScopeTenant, storedVersion.Scope)
			require.NotNil(t, storedVersion.TenantID)
			assert.Equal(t, testTenantID, *storedVersion.TenantID)
			require.NotNil(t, storedVersion.CreatedByUserID)
			assert.Equal(t, testUserID, *storedVersion.CreatedByUserID)
			assert.Equal(t, storedModule.ID, storedVersion.ModuleID)
			assert.Equal(t, domain.VersionStatusDraft, storedVersion.Status)
			assert.True(t, storedVersion.IsValid)
			assert.Equal(t, "<bpmn/>", storedVersion.BPMNXML)

			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, map[string]any{
				"module_id":  storedModule.ID.String(),
				"version_id": storedVersion.ID.String(),
				"status":     "DRAFT",
				"message":    "Module created",
			}, resp)
			assert.Equal(t, uuid.Version(7), storedModule.ID.Version())
			assert.Equal(t, uuid.Version(7), storedVersion.ID.Version())

			infos := log.recordedInfos()
			require.Len(t, infos, 1)
			assert.Equal(t, "module created", infos[0].msg)
			assert.Equal(t, map[string]any{
				"tenant_id": testTenantID.String(),
				"user_id":   testUserID.String(),
				"module_id": storedModule.ID.String(),
				"scope":     "tenant",
			}, infos[0].fields)
		})
	}
}
