package handler_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const starterCreatePath = "/api/v1/starters"

type starterCreateWiring struct {
	starters       *mocks.MockStarterTemplateRepository
	globalStarters *mocks.MockStarterTemplateRepository
	versions       *mocks.MockModuleVersionRepository
	globalVersions *mocks.MockModuleVersionRepository
	compiler       *mocks.MockPlanCompiler
	log            *starterCreateLogger
	router         *gin.Engine
}

type starterCreateLogEntry struct {
	msg    string
	fields map[string]any
}

type starterCreateLogger struct {
	mu   sync.Mutex
	warn []starterCreateLogEntry
}

func (*starterCreateLogger) Debug(string, map[string]any) { /* no-op */ }
func (*starterCreateLogger) Info(string, map[string]any)  { /* no-op */ }
func (*starterCreateLogger) Error(string, map[string]any) { /* no-op */ }
func (*starterCreateLogger) Fatal(string, map[string]any) { /* no-op */ }
func (l *starterCreateLogger) Warn(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warn = append(l.warn, starterCreateLogEntry{msg: msg, fields: fields})
}

func (l *starterCreateLogger) warnings(msg string) []starterCreateLogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []starterCreateLogEntry
	for _, e := range l.warn {
		if e.msg == msg {
			out = append(out, e)
		}
	}
	return out
}

func newStarterCreateWiring(t *testing.T) *starterCreateWiring {
	t.Helper()
	ctrl := gomock.NewController(t)
	w := &starterCreateWiring{
		starters:       mocks.NewMockStarterTemplateRepository(ctrl),
		globalStarters: mocks.NewMockStarterTemplateRepository(ctrl),
		versions:       mocks.NewMockModuleVersionRepository(ctrl),
		globalVersions: mocks.NewMockModuleVersionRepository(ctrl),
		compiler:       mocks.NewMockPlanCompiler(ctrl),
		log:            &starterCreateLogger{},
	}
	svc := service.NewStarterService(service.StarterDeps{
		Starters:             w.starters,
		GlobalStarters:       w.globalStarters,
		Versions:             mocks.NewMockWorkflowVersionRepository(ctrl),
		Compiler:             w.compiler,
		ModuleVersions:       w.versions,
		GlobalModuleVersions: w.globalVersions,
	})
	w.router = newRouter(handler.New(handler.Services{Starters: svc, Log: w.log}))
	return w
}

func starterCreateBody(scope string) map[string]any {
	body := map[string]any{"name": "Review", "bpmn_xml": "<bpmn/>"}
	if scope != "" {
		body["scope"] = scope
	}
	return body
}

func starterCreateCalls(callIDs ...string) []domain.ModuleCall {
	calls := make([]domain.ModuleCall, len(callIDs))
	for i, id := range callIDs {
		calls[i] = domain.ModuleCall{CallID: id, ProcessID: "Process_Missing"}
	}
	return calls
}

func starterCreateInvalidParams(t *testing.T, prob map[string]any) []map[string]any {
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

func starterCreateRawReq(raw string) *http.Request {
	r := adminReq(http.MethodPost, starterCreatePath, nil)
	r.Body = io.NopCloser(strings.NewReader(raw))
	return r
}

func TestStarterCreate_FieldLengths_OverTheLimitIsRefusedNamed(t *testing.T) {
	type param struct{ name, reason string }
	cases := []struct {
		name        string
		starterName string
		description string
		category    string
		want        []param
	}{
		{
			name:        "name one over the limit",
			starterName: strings.Repeat("n", 256),
			want:        []param{{"name", "must not exceed 255 characters"}},
		},
		{
			name:        "description one over the limit",
			starterName: "Review",
			description: strings.Repeat("d", 2001),
			want:        []param{{"description", "must not exceed 2000 characters"}},
		},
		{
			name:        "category one over the limit",
			starterName: "Review",
			category:    strings.Repeat("c", 65),
			want:        []param{{"category", "must not exceed 64 characters"}},
		},
		{
			name:        "all three over their limits are listed together in field order",
			starterName: strings.Repeat("n", 256),
			description: strings.Repeat("d", 2001),
			category:    strings.Repeat("c", 65),
			want: []param{
				{"name", "must not exceed 255 characters"},
				{"description", "must not exceed 2000 characters"},
				{"category", "must not exceed 64 characters"},
			},
		},
		{
			name:        "multibyte name one rune over the limit",
			starterName: strings.Repeat("é", 256),
			want:        []param{{"name", "must not exceed 255 characters"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			h := newStarterHandler(&fakeStarterSvc{
				create: func(context.Context, uuid.UUID, uuid.UUID, service.CreateStarterReq) (*domain.StarterTemplate, error) {
					called = true
					return &domain.StarterTemplate{ID: testWFID}, nil
				},
			})
			body := map[string]any{
				"name": tc.starterName, "description": tc.description,
				"category": tc.category, "bpmn_xml": "<bpmn/>",
			}

			w := do(newRouter(h), adminReq(http.MethodPost, starterCreatePath, body))

			require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "INVALID_INPUT", prob["code"])
			assert.Equal(t, "request fields exceed maximum length", prob["detail"])
			params := starterCreateInvalidParams(t, prob)
			require.Len(t, params, len(tc.want))
			for i, want := range tc.want {
				assert.Equal(t, want.name, params[i]["name"])
				assert.Equal(t, want.reason, params[i]["reason"])
			}
			assert.False(t, called, "an over-long field is rejected before the service runs")
		})
	}
}

func TestStarterCreate_FieldLengths_AtTheLimitIsAcceptedUnchanged(t *testing.T) {
	cases := []struct {
		name  string
		glyph string
	}{
		{name: "single-byte characters", glyph: "a"},
		{name: "multibyte characters count once each", glyph: "é"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []service.CreateStarterReq
			var gotTenant, gotUser uuid.UUID
			h := newStarterHandler(&fakeStarterSvc{
				create: func(_ context.Context, tenantID, userID uuid.UUID, r service.CreateStarterReq) (*domain.StarterTemplate, error) {
					got = append(got, r)
					gotTenant, gotUser = tenantID, userID
					return &domain.StarterTemplate{ID: testWFID, Scope: domain.ScopeTenant}, nil
				},
			})
			name := strings.Repeat(tc.glyph, 255)
			description := strings.Repeat(tc.glyph, 2000)
			category := strings.Repeat(tc.glyph, 64)
			body := map[string]any{
				"name": name, "description": description, "category": category, "bpmn_xml": "<bpmn/>",
			}

			w := do(newRouter(h), adminReq(http.MethodPost, starterCreatePath, body))

			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			require.Len(t, got, 1)
			assert.Equal(t, testTenantID, gotTenant)
			assert.Equal(t, testUserID, gotUser)
			assert.Equal(t, name, got[0].Name)
			assert.Equal(t, description, got[0].Description)
			assert.Equal(t, category, got[0].Category)
		})
	}
}

func TestStarterCreate_MalformedBody_Returns400(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantDetail string
		wantParams []map[string]any
	}{
		{name: "invalid JSON", raw: `{"name": "Review", "bpmn_xml": `, wantDetail: "request body is not valid JSON"},
		{name: "not an object", raw: `nope`, wantDetail: "request body is not valid JSON"},
		{name: "empty body", raw: ``, wantDetail: "request body is not valid JSON"},
		{
			name:       "data after the JSON value",
			raw:        `{"name":"Review","bpmn_xml":"<bpmn/>"} {"name":"again"}`,
			wantDetail: "request body is not valid JSON",
		},
		{
			name:       "a field of the wrong type",
			raw:        `{"name":123,"bpmn_xml":"<bpmn/>"}`,
			wantDetail: "request body has a field of the wrong type",
			wantParams: []map[string]any{{"name": "name", "reason": "has the wrong type"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			h := newStarterHandler(&fakeStarterSvc{
				create: func(context.Context, uuid.UUID, uuid.UUID, service.CreateStarterReq) (*domain.StarterTemplate, error) {
					called = true
					return &domain.StarterTemplate{ID: testWFID}, nil
				},
			})

			w := do(newRouter(h), starterCreateRawReq(tc.raw))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, tc.wantDetail, prob["detail"])
			if tc.wantParams == nil {
				assert.Nil(t, prob["invalid_params"])
			} else {
				params := starterCreateInvalidParams(t, prob)
				require.Len(t, params, len(tc.wantParams))
				for i, want := range tc.wantParams {
					assert.Equal(t, want["name"], params[i]["name"])
					assert.Equal(t, want["reason"], params[i]["reason"])
				}
			}
			assert.False(t, called, "a body that cannot be read never reaches the service")
		})
	}
}

func TestStarterCreate_BodyOverTenMegabytes_Returns413(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	r.Use(httpmiddleware.LimitRequestBody())
	called := false
	registerRoutes(r, newStarterHandler(&fakeStarterSvc{
		create: func(context.Context, uuid.UUID, uuid.UUID, service.CreateStarterReq) (*domain.StarterTemplate, error) {
			called = true
			return &domain.StarterTemplate{ID: testWFID}, nil
		},
	}))
	big := `{"name":"Review","bpmn_xml":"` + strings.Repeat("a", 10<<20+512) + `"}`

	w := do(r, starterCreateRawReq(big))

	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	prob := decodeProblem(t, w)
	assert.Equal(t, "PAYLOAD_TOO_LARGE", prob["code"])
	assert.Equal(t, "request body exceeds the 10 MB limit", prob["detail"])
	assert.False(t, called)
}

func TestStarterCreate_UnresolvedCall_Returns422AndStoresNothing(t *testing.T) {
	t.Run("tenant starter calling a module that is missing, unpublished or archived", func(t *testing.T) {
		w := newStarterCreateWiring(t)
		w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(starterCreateCalls("Call_1"), nil, nil)
		w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Missing", int32(0)).Return(nil, domain.ErrNotFound)

		rec := do(w.router, adminReq(http.MethodPost, starterCreatePath, starterCreateBody("")))

		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		prob := decodeProblem(t, rec)
		assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
		params := starterCreateInvalidParams(t, prob)
		require.Len(t, params, 1)
		assert.Equal(t, "Call_1", params[0]["name"])
		assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", params[0]["code"])
		assert.Contains(t, params[0]["reason"], "Process_Missing")
	})

	t.Run("global starter calling a module that exists only for a tenant", func(t *testing.T) {
		w := newStarterCreateWiring(t)
		w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(starterCreateCalls("Call_Tenant"), nil, nil)
		w.globalVersions.EXPECT().Resolve(gomock.Any(), uuid.Nil, "Process_Missing", int32(0)).Return(nil, domain.ErrNotFound)

		rec := do(w.router, platformOperatorReq(http.MethodPost, starterCreatePath, starterCreateBody("global")))

		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		prob := decodeProblem(t, rec)
		assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
		params := starterCreateInvalidParams(t, prob)
		require.Len(t, params, 1)
		assert.Equal(t, "Call_Tenant", params[0]["name"])
		assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", params[0]["code"])
	})

	t.Run("a called module that itself calls a missing module is reported on the starter's call", func(t *testing.T) {
		w := newStarterCreateWiring(t)
		number := int32(1)
		module := &domain.ModuleVersion{
			ID: uuid.New(), ModuleID: uuid.New(), ProcessID: "Process_A",
			VersionNumber: &number, BPMNXML: "<a/>",
		}
		w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").
			Return([]domain.ModuleCall{{CallID: "Call_Root", ProcessID: "Process_A"}}, nil, nil)
		w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_A", int32(0)).Return(module, nil)
		w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<a/>").
			Return([]domain.ModuleCall{{CallID: "Call_Inner", ProcessID: "Process_B"}}, nil, nil)
		w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_B", int32(0)).Return(nil, domain.ErrNotFound)

		rec := do(w.router, adminReq(http.MethodPost, starterCreatePath, starterCreateBody("")))

		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		prob := decodeProblem(t, rec)
		assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
		params := starterCreateInvalidParams(t, prob)
		require.Len(t, params, 1)
		assert.Equal(t, "Call_Root", params[0]["name"])
		assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", params[0]["code"])
		assert.Contains(t, params[0]["reason"], "Process_A")
		assert.Contains(t, params[0]["reason"], "Process_B")
	})

	t.Run("two calls naming the same missing module are reported once each after one lookup", func(t *testing.T) {
		w := newStarterCreateWiring(t)
		w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(starterCreateCalls("Call_A", "Call_B"), nil, nil)
		w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Missing", int32(0)).
			Return(nil, domain.ErrNotFound).Times(1)

		rec := do(w.router, adminReq(http.MethodPost, starterCreatePath, starterCreateBody("")))

		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		params := starterCreateInvalidParams(t, decodeProblem(t, rec))
		require.Len(t, params, 2)
		assert.Equal(t, "Call_A", params[0]["name"])
		assert.Equal(t, "Call_B", params[1]["name"])
		for _, p := range params {
			assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", p["code"])
		}
	})
}

func TestStarterCreate_CallIssuesFromTheCompiler_Returns422WithThoseIssues(t *testing.T) {
	w := newStarterCreateWiring(t)
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, []domain.BPMNValidationError{
		{Code: domain.BPMNErrorCode("CALL_ISSUE_ONE"), NodeID: "Call_1", Message: "first call is malformed", Severity: domain.SeverityError},
		{Code: domain.BPMNErrorCode("CALL_ISSUE_TWO"), NodeID: "Call_2", Message: "second call is malformed", Severity: domain.SeverityError},
	}, nil)

	rec := do(w.router, adminReq(http.MethodPost, starterCreatePath, starterCreateBody("")))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	prob := decodeProblem(t, rec)
	assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
	params := starterCreateInvalidParams(t, prob)
	require.Len(t, params, 2)
	assert.Equal(t, map[string]any{"name": "Call_1", "reason": "first call is malformed", "code": "CALL_ISSUE_ONE"}, params[0])
	assert.Equal(t, map[string]any{"name": "Call_2", "reason": "second call is malformed", "code": "CALL_ISSUE_TWO"}, params[1])
}

func TestStarterCreate_BlockingErrorBesideWarnings_Returns422(t *testing.T) {
	w := newStarterCreateWiring(t)
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	w.compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bundled/>", nil)
	w.compiler.EXPECT().Validate(gomock.Any(), "<bundled/>").Return([]domain.BPMNValidationError{
		{Code: domain.BPMNErrorCode("ONLY_A_WARNING"), NodeID: "Task_1", Message: "worth a look", Severity: domain.SeverityWarning},
		{Code: domain.BPMNErrNoStartEvent, NodeID: "Process_1", Message: "no start event", Severity: domain.SeverityError},
	}, nil)

	rec := do(w.router, adminReq(http.MethodPost, starterCreatePath, starterCreateBody("")))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	prob := decodeProblem(t, rec)
	assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
	var codes []any
	for _, p := range starterCreateInvalidParams(t, prob) {
		codes = append(codes, p["code"])
	}
	assert.Contains(t, codes, string(domain.BPMNErrNoStartEvent))
}

func TestStarterCreate_BPMNRefusedByTheCompiler_Returns400(t *testing.T) {
	cases := []struct {
		name  string
		setup func(w *starterCreateWiring)
	}{
		{
			name: "unparseable while listing the module calls",
			setup: func(w *starterCreateWiring) {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").
					Return(nil, nil, fmt.Errorf("%w: XML decode: unexpected EOF", domain.ErrMalformedBPMN))
			},
		},
		{
			name: "unparseable while validating",
			setup: func(w *starterCreateWiring) {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
				w.compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
				w.compiler.EXPECT().Validate(gomock.Any(), "<bpmn/>").
					Return(nil, fmt.Errorf("%w: XML decode: unexpected EOF", domain.ErrMalformedBPMN))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newStarterCreateWiring(t)
			tc.setup(w)

			rec := do(w.router, adminReq(http.MethodPost, starterCreatePath, starterCreateBody("")))

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			prob := decodeProblem(t, rec)
			assert.Equal(t, "INVALID_BPMN_XML", prob["code"])
			assert.Equal(t, "the BPMN document could not be parsed", prob["detail"])
			assert.NotContains(t, rec.Body.String(), "unexpected EOF", "the parser text is not sent to the caller")
			assert.Empty(t, w.log.warnings("forbidden XML construct rejected"), "a plain parse failure is not logged as an attack")
		})
	}
}

func TestStarterCreate_ForbiddenXMLConstruct_Returns400AndLogsTheCaller(t *testing.T) {
	w := newStarterCreateWiring(t)
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").
		Return(nil, nil, fmt.Errorf("%w: %w: DOCTYPE detected", domain.ErrMalformedBPMN, domain.ErrForbiddenXML))

	rec := do(w.router, adminReq(http.MethodPost, starterCreatePath, starterCreateBody("")))

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	prob := decodeProblem(t, rec)
	assert.Equal(t, "INVALID_BPMN_XML", prob["code"])
	assert.Equal(t, "the BPMN document could not be parsed", prob["detail"])
	assert.NotContains(t, rec.Body.String(), "DOCTYPE", "the rejected construct is not echoed to the caller")
	logged := w.log.warnings("forbidden XML construct rejected")
	require.Len(t, logged, 1)
	assert.Equal(t, testTenantID.String(), logged[0].fields["tenant_id"])
	assert.NotEmpty(t, logged[0].fields["client_ip"])
}

func TestStarterCreate_ModuleLookupFailure_MapsToTheDependencyStatus(t *testing.T) {
	const leaked = "pq: relation module_version is corrupt"
	cases := []struct {
		name       string
		lookupErr  error
		wantStatus int
		wantCode   string
		leakedText string
	}{
		{
			name:       "database error is an internal error without its text",
			lookupErr:  errors.New(leaked),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			leakedText: leaked,
		},
		{
			name:       "database unreachable is an unavailable upstream",
			lookupErr:  fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			leakedText: "connection refused",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newStarterCreateWiring(t)
			w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(starterCreateCalls("Call_1"), nil, nil)
			w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Missing", int32(0)).Return(nil, tc.lookupErr)

			rec := do(w.router, adminReq(http.MethodPost, starterCreatePath, starterCreateBody("")))

			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			assert.Equal(t, tc.wantCode, decodeProblem(t, rec)["code"])
			assert.NotContains(t, rec.Body.String(), tc.leakedText)
		})
	}
}

func TestStarterCreate_SameIdempotencyKey_ReplaysTheFirstCreation(t *testing.T) {
	created := 0
	svc := &fakeStarterSvc{
		create: func(_ context.Context, _, _ uuid.UUID, r service.CreateStarterReq) (*domain.StarterTemplate, error) {
			created++
			return &domain.StarterTemplate{ID: uuid.New(), Scope: domain.ScopeTenant, Name: r.Name}, nil
		},
	}
	router := newRouter(handler.New(handler.Services{
		Starters: svc, Cache: newMemCache(), IdempotencyTTL: time.Hour,
	}))
	send := func() *httptest.ResponseRecorder {
		r := adminReq(http.MethodPost, starterCreatePath, starterCreateBody(""))
		r.Header.Set("Idempotency-Key", "create-starter-key")
		return do(router, r)
	}

	first := send()
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	second := send()

	assert.Equal(t, http.StatusCreated, second.Code, second.Body.String())
	assert.JSONEq(t, first.Body.String(), second.Body.String(), "the replay carries the first starter's id")
	assert.Equal(t, 1, created, "the starter is created once")
}

var _ port.Logger = (*starterCreateLogger)(nil)
