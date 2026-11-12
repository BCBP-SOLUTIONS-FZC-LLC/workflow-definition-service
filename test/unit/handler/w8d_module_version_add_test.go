package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

const (
	w8dProcessID = "Process_W8d"

	w8dTypeBadRequest   = "https://api.workflow.platform/errors/bad-request"
	w8dTypeForbidden    = "https://api.workflow.platform/errors/forbidden"
	w8dTypeInternal     = "https://api.workflow.platform/errors/internal-error"
	w8dTypeUnavailable  = "https://api.workflow.platform/errors/service-unavailable"
	w8dDetailInternal   = "an unexpected error occurred"
	w8dDetailUnavail    = "an upstream dependency is temporarily unavailable"
	w8dDetailBadBody    = "request body has missing or invalid fields"
	w8dDetailBadXML     = "the BPMN document could not be parsed"
	w8dWarnForbiddenXML = "forbidden XML construct rejected"
)

type w8dProblem struct {
	Type          string `json:"type"`
	Status        int    `json:"status"`
	Detail        string `json:"detail"`
	Instance      string `json:"instance"`
	Code          string `json:"code"`
	InvalidParams []struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	} `json:"invalid_params"`
}

func w8dDecodeProblem(t *testing.T, w *httptest.ResponseRecorder) w8dProblem {
	t.Helper()
	var p w8dProblem
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p), w.Body.String())
	return p
}

type w8dLogLine struct {
	msg    string
	fields map[string]any
}

type w8dLog struct {
	mu    sync.Mutex
	warns []w8dLogLine
	errs  []w8dLogLine
	infos []w8dLogLine
}

func (l *w8dLog) Debug(string, map[string]any) { /* no-op */ }
func (l *w8dLog) Fatal(string, map[string]any) { /* no-op */ }
func (l *w8dLog) Info(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infos = append(l.infos, w8dLogLine{msg: msg, fields: fields})
}
func (l *w8dLog) Warn(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, w8dLogLine{msg: msg, fields: fields})
}
func (l *w8dLog) Error(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errs = append(l.errs, w8dLogLine{msg: msg, fields: fields})
}

func (l *w8dLog) lines(kind []w8dLogLine, msg string) []w8dLogLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []w8dLogLine
	for _, line := range kind {
		if line.msg == msg {
			out = append(out, line)
		}
	}
	return out
}

func (l *w8dLog) warnLines(msg string) []w8dLogLine {
	return l.lines(l.warns, msg)
}

func (l *w8dLog) errorLines(msg string) []w8dLogLine {
	return l.lines(l.errs, msg)
}

func (l *w8dLog) infoLines(msg string) []w8dLogLine {
	return l.lines(l.infos, msg)
}

var _ port.Logger = (*w8dLog)(nil)

type w8dTx struct {
	depth atomic.Int32
	runs  atomic.Int32
}

func (x *w8dTx) run(ctx context.Context, fn func(context.Context) error) error {
	x.runs.Add(1)
	x.depth.Add(1)
	defer x.depth.Add(-1)
	return fn(ctx)
}

func (x *w8dTx) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	return x.run(ctx, fn)
}

func (x *w8dTx) RunInTxWithRetry(ctx context.Context, fn func(context.Context) error) error {
	return x.run(ctx, fn)
}

func (x *w8dTx) inside() bool { return x.depth.Load() > 0 }

var _ port.Transactor = (*w8dTx)(nil)

type w8dModules struct {
	modules          *mocks.MockModuleRepository
	versions         *mocks.MockModuleVersionRepository
	workflowVersions *mocks.MockWorkflowVersionRepository
	compiler         *mocks.MockPlanCompiler
	tx               *w8dTx
	log              *w8dLog
	router           *gin.Engine
}

func w8dNewModules(t *testing.T, compiler port.PlanCompiler, cache port.CacheStore) *w8dModules {
	t.Helper()
	ctrl := gomock.NewController(t)
	w := &w8dModules{
		modules:          mocks.NewMockModuleRepository(ctrl),
		versions:         mocks.NewMockModuleVersionRepository(ctrl),
		workflowVersions: mocks.NewMockWorkflowVersionRepository(ctrl),
		compiler:         mocks.NewMockPlanCompiler(ctrl),
		tx:               &w8dTx{},
		log:              &w8dLog{},
	}
	if compiler == nil {
		compiler = w.compiler
	}
	svc := service.NewModuleService(service.ModuleDeps{
		Transactor:       w.tx,
		GlobalTransactor: w.tx,
		Modules:          w.modules,
		GlobalModules:    mocks.NewMockModuleRepository(ctrl),
		Versions:         w.versions,
		GlobalVersions:   mocks.NewMockModuleVersionRepository(ctrl),
		WorkflowVersions: w.workflowVersions,
		Compiler:         compiler,
		Log:              w.log,
	})
	services := handler.Services{Modules: svc, Log: w.log}
	if cache != nil {
		services.Cache = cache
		services.IdempotencyTTL = time.Hour
	}
	w.router = newRouter(handler.New(services))
	return w
}

func (w *w8dModules) expectModule(activeVersionID *uuid.UUID) {
	w.modules.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(&domain.Module{
		ID: testWFID, TenantID: &testTenantID, Scope: domain.ScopeTenant,
		ProcessID: w8dProcessID, ActiveVersionID: activeVersionID,
	}, nil).AnyTimes()
}

func (w *w8dModules) expectCleanCompile(xml string) {
	w.compiler.EXPECT().ProcessID(gomock.Any(), xml).Return(w8dProcessID, nil)
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), xml).Return(nil, nil, nil)
	w.compiler.EXPECT().BundleModules(xml, gomock.Any()).Return("<bundled/>", nil)
	w.compiler.EXPECT().ValidateModule(gomock.Any(), "<bundled/>", w8dProcessID, false).Return(nil, nil)
}

func w8dAddVersionPath() string {
	return "/api/v1/modules/" + testWFID.String() + "/versions"
}

func w8dRawAdminPost(path, raw string) *http.Request {
	r := adminReq(http.MethodPost, path, nil)
	r.Body = io.NopCloser(strings.NewReader(raw))
	return r
}

func w8dBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), w.Body.String())
	return body
}

func TestAddModuleVersion_DatabaseUnavailable_Returns503(t *testing.T) {
	t.Parallel()
	activeID := uuid.New()
	cases := []struct {
		name  string
		setup func(t *testing.T, w *w8dModules)
	}{
		{
			name: "the module read of the scope check",
			setup: func(_ *testing.T, w *w8dModules) {
				w.modules.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).
					Return(nil, fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable))
			},
		},
		{
			name: "the active version read of the scope check",
			setup: func(_ *testing.T, w *w8dModules) {
				w.expectModule(&activeID)
				w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, activeID).
					Return(nil, fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable))
			},
		},
		{
			name: "the lookup of a called module",
			setup: func(_ *testing.T, w *w8dModules) {
				w.expectModule(nil)
				w.compiler.EXPECT().ProcessID(gomock.Any(), "<x/>").Return(w8dProcessID, nil)
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<x/>").
					Return([]domain.ModuleCall{{CallID: "Call_1", ProcessID: "Process_Called"}}, nil, nil)
				w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Called", int32(0)).
					Return(nil, fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable))
			},
		},
		{
			name: "the insert of the new version",
			setup: func(t *testing.T, w *w8dModules) {
				w.expectModule(nil)
				w.expectCleanCompile("<x/>")
				w.versions.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, v *domain.ModuleVersion) error {
						assert.Equal(t, testWFID, v.ModuleID)
						require.NotNil(t, v.TenantID)
						assert.Equal(t, testTenantID, *v.TenantID)
						return fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable)
					})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := w8dNewModules(t, nil, nil)
			tc.setup(t, w)

			rec := do(w.router, adminReq(http.MethodPost, w8dAddVersionPath(), map[string]any{"bpmn_xml": "<x/>"}))

			require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
			p := w8dDecodeProblem(t, rec)
			assert.Equal(t, w8dTypeUnavailable, p.Type)
			assert.Equal(t, http.StatusServiceUnavailable, p.Status)
			assert.Equal(t, "UPSTREAM_UNAVAILABLE", p.Code)
			assert.Equal(t, w8dDetailUnavail, p.Detail)
			assert.Equal(t, w8dAddVersionPath(), p.Instance)
			assert.NotContains(t, rec.Body.String(), "connection refused")
			assert.Empty(t, w.log.infoLines("module version added"), "no success is logged for a failed add")
			logged := w.log.errorLines("request failed")
			require.Len(t, logged, 1)
			assert.EqualValues(t, http.StatusServiceUnavailable, logged[0].fields["status"])
			assert.Equal(t, testTenantID.String(), logged[0].fields["tenant_id"])
			assert.Equal(t, testUserID.String(), logged[0].fields["user_id"])
		})
	}
}

func TestAddModuleVersion_NoXMLInBody_CopiesTheActiveVersionForward(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
	}{
		{name: "empty object", raw: `{}`},
		{name: "bpmn_xml null", raw: `{"bpmn_xml":null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			activeID := uuid.New()
			w := w8dNewModules(t, nil, nil)
			w.expectModule(&activeID)
			w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, activeID).Return(&domain.ModuleVersion{
				ID: activeID, ModuleID: testWFID, BPMNXML: "<active/>", ProcessID: w8dProcessID,
			}, nil).MinTimes(1)
			w.expectCleanCompile("<active/>")
			var stored *domain.ModuleVersion
			w.versions.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, v *domain.ModuleVersion) error {
					stored = v
					return nil
				})

			rec := do(w.router, w8dRawAdminPost(w8dAddVersionPath(), tc.raw))

			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			require.NotNil(t, stored, "a new version is stored")
			assert.Equal(t, "<active/>", stored.BPMNXML, "the XML of the active version is copied")
			assert.NotEqual(t, activeID, stored.ID, "the copy is a new version")
			assert.Equal(t, testWFID, stored.ModuleID)
			assert.Equal(t, domain.VersionStatusDraft, stored.Status)
			assert.Equal(t, w8dProcessID, stored.ProcessID)
			require.NotNil(t, stored.CreatedByUserID)
			assert.Equal(t, testUserID, *stored.CreatedByUserID)
			require.NotNil(t, stored.TenantID)
			assert.Equal(t, testTenantID, *stored.TenantID)
			body := w8dBody(t, rec)
			assert.Equal(t, testWFID.String(), body["module_id"])
			assert.Equal(t, stored.ID.String(), body["version_id"])
			assert.Equal(t, "DRAFT", body["status"])
			assert.Equal(t, "Module version added", body["message"])
		})
	}
}

func TestAddModuleVersion_ForbiddenXMLConstruct_Returns400AndLogsTheCaller(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		xml      string
		wantWarn int
		leaked   string
	}{
		{
			name:     "document type with an external entity",
			xml:      `<?xml version="1.0"?><!DOCTYPE definitions [<!ENTITY zzqleak SYSTEM "file:///zzq/secret">]><definitions>&zzqleak;</definitions>`,
			wantWarn: 1,
			leaked:   "zzqleak",
		},
		{
			name:     "document type without entities",
			xml:      `<!DOCTYPE definitions><definitions/>`,
			wantWarn: 1,
			leaked:   "DOCTYPE",
		},
		{
			name:     "malformed XML without a forbidden construct",
			xml:      `<definitions`,
			wantWarn: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := w8dNewModules(t, bpmn_compiler.New(), nil)
			w.expectModule(nil)

			rec := do(w.router, adminReq(http.MethodPost, w8dAddVersionPath(), map[string]any{"bpmn_xml": tc.xml}))

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			p := w8dDecodeProblem(t, rec)
			assert.Equal(t, w8dTypeBadRequest, p.Type)
			assert.Equal(t, "INVALID_BPMN_XML", p.Code)
			assert.Equal(t, w8dDetailBadXML, p.Detail)
			assert.Equal(t, w8dAddVersionPath(), p.Instance)
			if tc.leaked != "" {
				assert.NotContains(t, rec.Body.String(), tc.leaked, "the rejected construct is not echoed")
			}
			warned := w.log.warnLines(w8dWarnForbiddenXML)
			require.Len(t, warned, tc.wantWarn)
			if tc.wantWarn > 0 {
				assert.Equal(t, testTenantID.String(), warned[0].fields["tenant_id"])
				assert.NotEmpty(t, warned[0].fields["client_ip"])
			}
		})
	}
}
