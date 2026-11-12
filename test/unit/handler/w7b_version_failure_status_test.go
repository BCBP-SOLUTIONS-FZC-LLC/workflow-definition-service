package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const (
	w7bTypeInternal    = "https://api.workflow.platform/errors/internal-error"
	w7bTypeUnavailable = "https://api.workflow.platform/errors/service-unavailable"
	w7bTypeNotFound    = "https://api.workflow.platform/errors/not-found"
	w7bTypeConflict    = "https://api.workflow.platform/errors/conflict"

	w7bDetailInternal    = "an unexpected error occurred"
	w7bDetailUnavailable = "an upstream dependency is temporarily unavailable"
)

type w7bProblem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
	Code     string `json:"code"`
}

type w7bLogLine struct {
	msg    string
	fields map[string]any
}

type w7bLog struct {
	mu   sync.Mutex
	errs []w7bLogLine
}

func (l *w7bLog) Debug(string, map[string]any) {}
func (l *w7bLog) Info(string, map[string]any)  {}
func (l *w7bLog) Warn(string, map[string]any)  {}
func (l *w7bLog) Fatal(string, map[string]any) {}
func (l *w7bLog) Error(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errs = append(l.errs, w7bLogLine{msg: msg, fields: fields})
}

func (l *w7bLog) errors() []w7bLogLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]w7bLogLine(nil), l.errs...)
}

type w7bLimits struct {
	mu    sync.Mutex
	limit *int64
	err   error
	asked []string
}

func (l *w7bLimits) WorkflowTemplateLimit(_ context.Context, _ uuid.UUID, planCode string) (*int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.asked = append(l.asked, planCode)
	return l.limit, l.err
}

func (l *w7bLimits) planCodes() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.asked...)
}

type w7bPlanReader struct {
	err   error
	calls atomic.Int32
}

func (r *w7bPlanReader) ReadPlanTable(context.Context) (map[string]*int64, error) {
	r.calls.Add(1)
	return nil, r.err
}

type w7bFixture struct {
	workflows *mocks.MockWorkflowRepository
	versions  *mocks.MockWorkflowVersionRepository
	compiler  *mocks.MockPlanCompiler
	tx        *mocks.MockTransactor
	log       *w7bLog

	mu         sync.Mutex
	txOutcomes []error
}

func w7bNewFixture(t *testing.T) *w7bFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	return &w7bFixture{
		workflows: mocks.NewMockWorkflowRepository(ctrl),
		versions:  mocks.NewMockWorkflowVersionRepository(ctrl),
		compiler:  mocks.NewMockPlanCompiler(ctrl),
		tx:        mocks.NewMockTransactor(ctrl),
		log:       &w7bLog{},
	}
}

func (f *w7bFixture) expectTransaction() {
	f.tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error {
			err := fn(ctx)
			f.mu.Lock()
			defer f.mu.Unlock()
			f.txOutcomes = append(f.txOutcomes, err)
			return err
		})
}

func (f *w7bFixture) outcomes() []error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]error(nil), f.txOutcomes...)
}

func (f *w7bFixture) router(limits port.PlanLimits) *gin.Engine {
	svc := service.NewVersionService(service.VersionDeps{
		PlanLimits: limits,
		Transactor: f.tx,
		Workflows:  f.workflows,
		Versions:   f.versions,
		Compiler:   f.compiler,
	})
	return newRouter(handler.New(handler.Services{Versions: svc, Log: f.log}))
}

func w7bVersionPath(action string) string {
	return "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/" + action
}

func w7bDecodeProblem(t *testing.T, w *httptest.ResponseRecorder) w7bProblem {
	t.Helper()
	require.Equal(t, "application/problem+json", strings.Split(w.Header().Get("Content-Type"), ";")[0], w.Body.String())
	var p w7bProblem
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p), w.Body.String())
	return p
}

func w7bRequireOneErrorLog(t *testing.T, log *w7bLog, status int, code, path, causeText string) {
	t.Helper()
	lines := log.errors()
	require.Len(t, lines, 1, "a failure that answers %d is logged once", status)
	assert.Equal(t, "request failed", lines[0].msg)
	assert.EqualValues(t, status, lines[0].fields["status"])
	assert.Equal(t, code, lines[0].fields["code"])
	assert.Equal(t, testTenantID.String(), lines[0].fields["tenant_id"])
	assert.Equal(t, testUserID.String(), lines[0].fields["user_id"])
	assert.Equal(t, path, lines[0].fields["path"])
	errText, _ := lines[0].fields["error"].(string)
	assert.Contains(t, errText, causeText, "the log carries the cause the caller never sees")
}

func w7bAssertDetail(t *testing.T, got, want string, partial bool) {
	t.Helper()
	if partial {
		assert.Contains(t, got, want)
		return
	}
	assert.Equal(t, want, got)
}

func w7bDraftOf(xml string) *domain.WorkflowVersion {
	v := newDraftVersion()
	v.BPMNXML = xml
	return v
}

func TestPublishVersionHTTP_FailureBeforeAnythingIsWritten_IsMappedToItsStatus(t *testing.T) {
	t.Parallel()

	const xml = `<definitions id="w7b-publish-failure"/>`
	cases := []struct {
		name           string
		lookupErr      error
		moduleCallsErr error
		bundleErr      error
		wantStatus     int
		wantCode       string
		wantType       string
		wantTitle      string
		wantDetail     string
		partialDetail  bool
		wantLogged     string
	}{
		{
			name:           "listing the module calls fails",
			moduleCallsErr: errors.New("w7b module calls refused"),
			wantStatus:     http.StatusInternalServerError,
			wantCode:       "INTERNAL_ERROR",
			wantType:       w7bTypeInternal,
			wantTitle:      "Internal Server Error",
			wantDetail:     w7bDetailInternal,
			wantLogged:     "list module calls: w7b module calls refused",
		},
		{
			name:       "bundling the modules fails",
			bundleErr:  errors.New("w7b bundle refused"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantType:   w7bTypeInternal,
			wantTitle:  "Internal Server Error",
			wantDetail: w7bDetailInternal,
			wantLogged: "bundle modules: w7b bundle refused",
		},
		{
			name:          "the version does not exist",
			lookupErr:     domain.ErrNotFound,
			wantStatus:    http.StatusNotFound,
			wantCode:      "NOT_FOUND",
			wantType:      w7bTypeNotFound,
			wantTitle:     "Resource Not Found",
			wantDetail:    "resource not found",
			partialDetail: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := w7bNewFixture(t)
			if tc.lookupErr != nil {
				f.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(nil, tc.lookupErr)
			} else {
				f.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(w7bDraftOf(xml), nil)
				f.compiler.EXPECT().ModuleCalls(gomock.Any(), xml).Return(nil, nil, tc.moduleCallsErr)
				if tc.moduleCallsErr == nil {
					f.compiler.EXPECT().BundleModules(xml, []domain.ResolvedModule(nil)).Return("", tc.bundleErr)
				}
			}

			w := do(f.router(&w7bLimits{}), adminReq(http.MethodPost, w7bVersionPath("publish"), nil))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			p := w7bDecodeProblem(t, w)
			w7bAssertDetail(t, p.Detail, tc.wantDetail, tc.partialDetail)
			assert.Equal(t, w7bProblem{
				Type: tc.wantType, Title: tc.wantTitle, Status: tc.wantStatus,
				Detail: p.Detail, Instance: w7bVersionPath("publish"), Code: tc.wantCode,
			}, p)
			if tc.wantLogged != "" {
				w7bRequireOneErrorLog(t, f.log, tc.wantStatus, tc.wantCode, w7bVersionPath("publish"), tc.wantLogged)
				assert.NotContains(t, w.Body.String(), "w7b", "the cause stays out of the response")
				return
			}
			assert.Empty(t, f.log.errors(), "a 404 is not logged as a failure")
		})
	}
}

func w7bPublishedSource() *domain.WorkflowVersion {
	v := newPublishedVersion()
	v.BPMNXML = `<definitions id="w7b-clone-source"/>`
	return v
}

func w7bCloneRequest(planCode string) *http.Request {
	r := adminReq(http.MethodPost, w7bVersionPath("clone"), map[string]string{
		"new_key": "w7b-copy-key", "new_name": "W7b copy",
	})
	r.Header.Set("x-plan", planCode)
	return r
}

func TestCloneVersionHTTP_SourceLookupFails_IsMappedAndNothingElseRuns(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		lookupErr  error
		wantStatus int
		wantCode   string
		wantType   string
		wantDetail string
		partial    bool
		wantLogged string
	}{
		{
			name:       "the database cannot be reached",
			lookupErr:  fmt.Errorf("%w: w7b connection refused", domain.ErrUpstreamUnavailable),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantType:   w7bTypeUnavailable,
			wantDetail: w7bDetailUnavailable,
			wantLogged: "get source version: upstream service unavailable: w7b connection refused",
		},
		{
			name:       "the database fails for another reason",
			lookupErr:  errors.New("w7b source read refused"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantType:   w7bTypeInternal,
			wantDetail: w7bDetailInternal,
			wantLogged: "get source version: w7b source read refused",
		},
		{
			name:       "the version does not exist",
			lookupErr:  domain.ErrNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   "NOT_FOUND",
			wantType:   w7bTypeNotFound,
			wantDetail: "resource not found",
			partial:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := w7bNewFixture(t)
			limits := &w7bLimits{}
			f.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(nil, tc.lookupErr)

			w := do(f.router(limits), w7bCloneRequest("starter"))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			p := w7bDecodeProblem(t, w)
			assert.Equal(t, tc.wantType, p.Type)
			assert.Equal(t, tc.wantCode, p.Code)
			assert.Equal(t, tc.wantStatus, p.Status)
			w7bAssertDetail(t, p.Detail, tc.wantDetail, tc.partial)
			assert.Equal(t, w7bVersionPath("clone"), p.Instance)
			assert.Empty(t, limits.planCodes(), "the plan is not looked up for a source that cannot be read")
			assert.Empty(t, f.outcomes(), "no transaction is opened")
			if tc.wantLogged == "" {
				assert.Empty(t, f.log.errors(), "a 404 is not logged as a failure")
				return
			}
			w7bRequireOneErrorLog(t, f.log, tc.wantStatus, tc.wantCode, w7bVersionPath("clone"), tc.wantLogged)
			assert.NotContains(t, w.Body.String(), "w7b", "the cause stays out of the response")
		})
	}
}

func TestCloneVersionHTTP_StepOfTheTransactionFails_IsMappedAndTheTransactionEndsInError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		countErr     error
		workflowErr  error
		versionErr   error
		wantWorkflow bool
		wantVersion  bool
		wantStatus   int
		wantCode     string
		wantType     string
		wantDetail   string
		partial      bool
		wantLogged   string
	}{
		{
			name:       "counting the tenant's workflows fails",
			countErr:   errors.New("w7b count refused"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantType:   w7bTypeInternal,
			wantDetail: w7bDetailInternal,
			wantLogged: "count workflows: w7b count refused",
		},
		{
			name:       "counting the tenant's workflows loses the database",
			countErr:   fmt.Errorf("%w: w7b connection lost", domain.ErrUpstreamUnavailable),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantType:   w7bTypeUnavailable,
			wantDetail: w7bDetailUnavailable,
			wantLogged: "count workflows: upstream service unavailable: w7b connection lost",
		},
		{
			name:         "the workflow insert fails with an error that is not a duplicate key",
			workflowErr:  errors.New("w7b workflow insert refused"),
			wantWorkflow: true,
			wantStatus:   http.StatusInternalServerError,
			wantCode:     "INTERNAL_ERROR",
			wantType:     w7bTypeInternal,
			wantDetail:   w7bDetailInternal,
			wantLogged:   "clone version: w7b workflow insert refused",
		},
		{
			name:         "the workflow insert collides with an existing template key",
			workflowErr:  fmt.Errorf("insert workflow: %w", domain.ErrDuplicateTemplateKey),
			wantWorkflow: true,
			wantStatus:   http.StatusConflict,
			wantCode:     "DUPLICATE_TEMPLATE_KEY",
			wantType:     w7bTypeConflict,
			wantDetail:   "already exists",
			partial:      true,
		},
		{
			name:         "the version insert fails after the workflow insert",
			versionErr:   errors.New("w7b version insert refused"),
			wantWorkflow: true,
			wantVersion:  true,
			wantStatus:   http.StatusInternalServerError,
			wantCode:     "INTERNAL_ERROR",
			wantType:     w7bTypeInternal,
			wantDetail:   w7bDetailInternal,
			wantLogged:   "clone version: w7b version insert refused",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := w7bNewFixture(t)
			five := int64(5)
			limits := &w7bLimits{limit: &five}
			f.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(w7bPublishedSource(), nil)
			f.expectTransaction()
			count := int64(4)
			if tc.countErr != nil {
				count = 0
			}
			f.workflows.EXPECT().CountByTenant(gomock.Any(), testTenantID).Return(count, tc.countErr)
			var createdWorkflow *domain.Workflow
			if tc.wantWorkflow {
				f.workflows.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, wf *domain.Workflow) error {
						createdWorkflow = wf
						return tc.workflowErr
					})
			}
			var createdVersion *domain.WorkflowVersion
			if tc.wantVersion {
				f.versions.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, v *domain.WorkflowVersion) error {
						createdVersion = v
						return tc.versionErr
					})
			}

			w := do(f.router(limits), w7bCloneRequest("starter"))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			p := w7bDecodeProblem(t, w)
			assert.Equal(t, tc.wantType, p.Type)
			assert.Equal(t, tc.wantCode, p.Code)
			assert.Equal(t, tc.wantStatus, p.Status)
			w7bAssertDetail(t, p.Detail, tc.wantDetail, tc.partial)
			assert.Equal(t, w7bVersionPath("clone"), p.Instance)
			assert.NotContains(t, w.Body.String(), "version_id", "a refused clone answers no ids")
			assert.Equal(t, []string{"starter"}, limits.planCodes())

			outcomes := f.outcomes()
			require.Len(t, outcomes, 1)
			require.Error(t, outcomes[0], "the failing step leaves the transaction through its error, so the writes roll back")
			if tc.wantWorkflow {
				require.NotNil(t, createdWorkflow)
				assert.Equal(t, testTenantID, createdWorkflow.TenantID)
				assert.Equal(t, "w7b-copy-key", createdWorkflow.TemplateKey)
				assert.Equal(t, testUserID, createdWorkflow.CreatedByUserID)
			}
			if tc.wantVersion {
				require.NotNil(t, createdVersion)
				assert.Equal(t, createdWorkflow.ID, createdVersion.WorkflowID)
				assert.Equal(t, testTenantID, createdVersion.TenantID)
				assert.Equal(t, w7bPublishedSource().BPMNXML, createdVersion.BPMNXML)
				assert.Equal(t, testUserID, createdVersion.CreatedByUserID)
				assert.Equal(t, domain.VersionStatusDraft, createdVersion.Status)
			}
			if tc.wantLogged == "" {
				assert.Empty(t, f.log.errors(), "a 409 is not logged as a failure")
				return
			}
			w7bRequireOneErrorLog(t, f.log, tc.wantStatus, tc.wantCode, w7bVersionPath("clone"), tc.wantLogged)
			assert.NotContains(t, w.Body.String(), "w7b", "the cause stays out of the response")
		})
	}
}

func w7bPromoteRequest(planCode string) *http.Request {
	r := adminReq(http.MethodPost, w7bVersionPath("promote"), nil)
	if planCode != "" {
		r.Header.Set("x-plan", planCode)
	}
	return r
}

func w7bExpectFirstActivationReads(f *w7bFixture, workflowReads int) {
	f.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(newPublishedVersion(), nil)
	f.workflows.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(newWorkflow(), nil).Times(workflowReads)
}

func TestPromoteVersionHTTP_FirstActivationWithoutAUsablePlanLimit_AnswersUnavailableAndActivatesNothing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		planHeader string
		noSource   bool
		tableErr   error
		wantReads  int32
		wantLogged string
	}{
		{name: "no x-plan header", planHeader: "", wantLogged: "no plan code on the request"},
		{name: "a blank x-plan header", planHeader: "   ", wantLogged: "no plan code on the request"},
		{
			name: "the plan table cannot be read and none is cached", planHeader: "starter",
			tableErr: errors.New("w7b plan table unreachable"), wantReads: 1,
			wantLogged: "no plan table has been read: w7b plan table unreachable",
		},
		{
			name: "no plan limit source is configured", planHeader: "starter", noSource: true,
			wantLogged: "no plan limit source is configured",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := w7bNewFixture(t)
			w7bExpectFirstActivationReads(f, 1)
			reader := &w7bPlanReader{err: tc.tableErr}
			var limits port.PlanLimits
			if !tc.noSource {
				limits = service.NewPlanLimitService(service.PlanLimitDeps{Reader: reader, TTL: time.Minute})
			}

			w := do(f.router(limits), w7bPromoteRequest(tc.planHeader))

			require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
			p := w7bDecodeProblem(t, w)
			assert.Equal(t, w7bTypeUnavailable, p.Type)
			assert.Equal(t, "UPSTREAM_UNAVAILABLE", p.Code)
			assert.Equal(t, http.StatusServiceUnavailable, p.Status)
			assert.Equal(t, w7bDetailUnavailable, p.Detail)
			assert.Equal(t, w7bVersionPath("promote"), p.Instance)
			assert.NotContains(t, w.Body.String(), "w7b", "the cause stays out of the response")
			assert.Equal(t, tc.wantReads, reader.calls.Load(), "the plan table is read only when there is a plan code to look up")
			assert.Empty(t, f.outcomes(), "no transaction is opened, so no active version is written")
			w7bRequireOneErrorLog(t, f.log, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", w7bVersionPath("promote"), tc.wantLogged)
		})
	}
}

func TestPromoteVersionHTTP_FirstActivationOnAnUnlimitedPlan_ReadsTheDraftFirstThenActivatesWithoutCountingWorkflows(t *testing.T) {
	t.Parallel()

	f := w7bNewFixture(t)
	w7bExpectFirstActivationReads(f, 2)
	f.expectTransaction()
	f.versions.EXPECT().GetDraft(gomock.Any(), testTenantID, testWFID).Return(nil, domain.ErrNotFound)
	activated := testVerID
	var gotActive *uuid.UUID
	f.workflows.EXPECT().UpdateActiveVersion(gomock.Any(), testTenantID, testWFID, &activated).DoAndReturn(
		func(_ context.Context, _, _ uuid.UUID, versionID *uuid.UUID) error {
			gotActive = versionID
			return nil
		})
	limits := &w7bLimits{}

	w := do(f.router(limits), w7bPromoteRequest("enterprise"))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, testWFID.String(), body["workflow_id"])
	assert.Equal(t, testVerID.String(), body["active_version_id"])
	assert.EqualValues(t, 1, body["version_number"])
	assert.Equal(t, "Version promoted to active", body["message"])
	require.NotNil(t, gotActive)
	assert.Equal(t, testVerID, *gotActive)
	assert.Equal(t, []string{"enterprise"}, limits.planCodes())
	outcomes := f.outcomes()
	require.Len(t, outcomes, 1)
	assert.NoError(t, outcomes[0])
	assert.Empty(t, f.log.errors())
}

func TestPromoteVersionHTTP_QuotaCheckStepFails_IsMappedAndTheActivationIsNotWritten(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		draftErr   error
		countErr   error
		wantStatus int
		wantCode   string
		wantType   string
		wantDetail string
		wantLogged string
	}{
		{
			name:       "the draft lookup fails",
			draftErr:   errors.New("w7b draft lookup refused"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantType:   w7bTypeInternal,
			wantDetail: w7bDetailInternal,
			wantLogged: "promote version: get draft: w7b draft lookup refused",
		},
		{
			name:       "the draft lookup loses the database",
			draftErr:   fmt.Errorf("%w: w7b connection lost", domain.ErrUpstreamUnavailable),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantType:   w7bTypeUnavailable,
			wantDetail: w7bDetailUnavailable,
			wantLogged: "promote version: get draft: upstream service unavailable: w7b connection lost",
		},
		{
			name:       "counting the tenant's workflows fails",
			draftErr:   domain.ErrNotFound,
			countErr:   errors.New("w7b count refused"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantType:   w7bTypeInternal,
			wantDetail: w7bDetailInternal,
			wantLogged: "promote version: count workflows: w7b count refused",
		},
		{
			name:       "counting the tenant's workflows loses the database",
			draftErr:   domain.ErrNotFound,
			countErr:   fmt.Errorf("%w: w7b connection lost", domain.ErrUpstreamUnavailable),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantType:   w7bTypeUnavailable,
			wantDetail: w7bDetailUnavailable,
			wantLogged: "promote version: count workflows: upstream service unavailable: w7b connection lost",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := w7bNewFixture(t)
			w7bExpectFirstActivationReads(f, 2)
			f.expectTransaction()
			f.versions.EXPECT().GetDraft(gomock.Any(), testTenantID, testWFID).Return(nil, tc.draftErr)
			if tc.countErr != nil {
				f.workflows.EXPECT().CountByTenant(gomock.Any(), testTenantID).Return(int64(0), tc.countErr)
			}
			five := int64(5)

			w := do(f.router(&w7bLimits{limit: &five}), w7bPromoteRequest("starter"))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			p := w7bDecodeProblem(t, w)
			assert.Equal(t, tc.wantType, p.Type)
			assert.Equal(t, tc.wantCode, p.Code)
			assert.Equal(t, tc.wantStatus, p.Status)
			assert.Equal(t, tc.wantDetail, p.Detail)
			assert.Equal(t, w7bVersionPath("promote"), p.Instance)
			assert.NotContains(t, w.Body.String(), "w7b", "the cause stays out of the response")
			assert.NotContains(t, w.Body.String(), "active_version_id")
			outcomes := f.outcomes()
			require.Len(t, outcomes, 1)
			require.Error(t, outcomes[0], "the transaction ends in the error, so the activation is rolled back")
			w7bRequireOneErrorLog(t, f.log, tc.wantStatus, tc.wantCode, w7bVersionPath("promote"), tc.wantLogged)
		})
	}
}
