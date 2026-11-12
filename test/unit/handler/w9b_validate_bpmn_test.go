package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

const (
	w9bValidatePath = "/api/v1/workflows/validate"
	w9bTypeInternal = "https://api.workflow.platform/errors/internal-error"
	w9bTypeUnavail  = "https://api.workflow.platform/errors/service-unavailable"
)

type w9bCallSpec struct {
	id      string
	process string
	pin     string
}

func w9bCallerXML(processID string, calls ...w9bCallSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_%[1]s" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="%[1]s" name="%[1]s" isExecutable="true">
    <bpmn:startEvent id="%[1]s_Start"/>
`, processID)
	previous := processID + "_Start"
	var flows strings.Builder
	for i, call := range calls {
		binding := ""
		if call.pin != "" {
			binding = fmt.Sprintf(` bindingType="versionTag" versionTag=%q`, call.pin)
		}
		fmt.Fprintf(&b, `    <bpmn:callActivity id="%[1]s" name="Call %[2]s">
      <bpmn:extensionElements>
        <zeebe:calledElement processId="%[2]s"%[3]s propagateAllChildVariables="false"/>
      </bpmn:extensionElements>
    </bpmn:callActivity>
`, call.id, call.process, binding)
		fmt.Fprintf(&flows, `    <bpmn:sequenceFlow id="%[1]s_F%[2]d" sourceRef="%[3]s" targetRef="%[4]s"/>
`, processID, i, previous, call.id)
		previous = call.id
	}
	fmt.Fprintf(&b, `    <bpmn:endEvent id="%[1]s_End"/>
%[2]s    <bpmn:sequenceFlow id="%[1]s_FEnd" sourceRef="%[3]s" targetRef="%[1]s_End"/>
  </bpmn:process>
</bpmn:definitions>`, processID, flows.String(), previous)
	return b.String()
}

func w9bPublishedModule(processID string, number int32, bpmnXML string) *domain.ModuleVersion {
	n := number
	return &domain.ModuleVersion{
		ID: uuid.New(), ModuleID: uuid.New(), ProcessID: processID, VersionNumber: &n,
		Status: domain.VersionStatusPublished, BPMNXML: bpmnXML,
	}
}

type w9bValidateWire struct {
	compiler *mocks.MockPlanCompiler
	modules  *mocks.MockModuleVersionRepository
	log      *w8dLog
	router   *gin.Engine
}

func w9bNewValidateWire(t *testing.T, compiler port.PlanCompiler) *w9bValidateWire {
	t.Helper()
	ctrl := gomock.NewController(t)
	w := &w9bValidateWire{
		compiler: mocks.NewMockPlanCompiler(ctrl),
		modules:  mocks.NewMockModuleVersionRepository(ctrl),
		log:      &w8dLog{},
	}
	if compiler == nil {
		compiler = w.compiler
	}
	svc := service.NewValidationService(service.ValidationDeps{Compiler: compiler, ModuleVersions: w.modules, Log: w.log})
	w.router = newRouter(handler.New(handler.Services{Validation: svc, Log: w.log}))
	return w
}

func (w *w9bValidateWire) expectNoModuleCalls(xml, bundled string, issues []domain.BPMNValidationError) {
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), xml).Return(nil, nil, nil)
	w.compiler.EXPECT().BundleModules(xml, []domain.ResolvedModule(nil)).Return(bundled, nil)
	w.compiler.EXPECT().ValidateForPublish(gomock.Any(), bundled).Return(issues, nil)
}

type w9bIssue struct {
	NodeID   string `json:"node_id"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

type w9bValidateBody struct {
	IsValid bool       `json:"is_valid"`
	Issues  []w9bIssue `json:"issues"`
}

func w9bValidateReq(bpmnXML string) *http.Request {
	return req(http.MethodPost, w9bValidatePath, map[string]string{"bpmn_xml": bpmnXML})
}

func w9bDecodeValidate(t *testing.T, rec *httptest.ResponseRecorder) w9bValidateBody {
	t.Helper()
	var body w9bValidateBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return body
}

func w9bIssueOf(e domain.BPMNValidationError) w9bIssue {
	return w9bIssue{NodeID: e.NodeID, Code: string(e.Code), Message: e.Message, Severity: string(e.Severity)}
}

func w9bRegisterMetrics(t *testing.T) {
	t.Helper()
	w6bRegisterMetrics(t)
}

func TestValidateBPMN_ABlockingIssue_Returns200InvalidAndCountsOneFailure(t *testing.T) {
	w9bRegisterMetrics(t)
	cases := []struct {
		name  string
		issue domain.BPMNValidationError
	}{
		{"an error-severity issue", domain.BPMNValidationError{
			Code: domain.BPMNErrNoStartEvent, NodeID: "Process_1", Message: "zzq no start event", Severity: domain.SeverityError,
		}},
		{"a severity that is not warning", domain.BPMNValidationError{
			Code: domain.BPMNErrUnmatchedGateway, NodeID: "GW_1", Message: "zzq gateway", Severity: domain.BPMNValidationSeverity("zzq_unknown"),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wire := w9bNewValidateWire(t, nil)
			wire.expectNoModuleCalls("<zzq/>", "<zzq-bundled/>", []domain.BPMNValidationError{tc.issue})
			before := testutil.ToFloat64(observability.WFValidationFailuresTotal)

			rec := do(wire.router, w9bValidateReq("<zzq/>"))

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			body := w9bDecodeValidate(t, rec)
			assert.False(t, body.IsValid)
			assert.Equal(t, []w9bIssue{w9bIssueOf(tc.issue)}, body.Issues)
			assert.Equal(t, before+1, testutil.ToFloat64(observability.WFValidationFailuresTotal),
				"a blocking answer counts as one validation failure")
			assert.Empty(t, wire.log.errorLines("request failed"), "a finding is an answer, not a failed request")
		})
	}
}

func TestValidateBPMN_WarningsAndErrorsMixed_Returns200InvalidWithEveryIssueInCompilerOrder(t *testing.T) {
	w9bRegisterMetrics(t)
	warning := func(node string) domain.BPMNValidationError {
		return domain.BPMNValidationError{Code: domain.BPMNWarnUnknownStageType, NodeID: node, Message: "zzq warning " + node, Severity: domain.SeverityWarning}
	}
	blocking := func(node string) domain.BPMNValidationError {
		return domain.BPMNValidationError{Code: domain.BPMNErrNoStartEvent, NodeID: node, Message: "zzq error " + node, Severity: domain.SeverityError}
	}
	cases := []struct {
		name      string
		issues    []domain.BPMNValidationError
		wantValid bool
		wantDelta float64
	}{
		{"a warning first then an error", []domain.BPMNValidationError{warning("T1"), blocking("P1")}, false, 1},
		{"an error first then a warning", []domain.BPMNValidationError{blocking("P1"), warning("T1")}, false, 1},
		{"an error between two warnings", []domain.BPMNValidationError{warning("T1"), blocking("P1"), warning("T2")}, false, 1},
		{"warnings only", []domain.BPMNValidationError{warning("T1"), warning("T2")}, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wire := w9bNewValidateWire(t, nil)
			wire.expectNoModuleCalls("<zzq/>", "<zzq-bundled/>", tc.issues)
			before := testutil.ToFloat64(observability.WFValidationFailuresTotal)

			rec := do(wire.router, w9bValidateReq("<zzq/>"))

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			body := w9bDecodeValidate(t, rec)
			assert.Equal(t, tc.wantValid, body.IsValid)
			want := make([]w9bIssue, len(tc.issues))
			for i, e := range tc.issues {
				want[i] = w9bIssueOf(e)
			}
			assert.Equal(t, want, body.Issues, "every issue is answered, in the order the compiler returned them")
			assert.Equal(t, before+tc.wantDelta, testutil.ToFloat64(observability.WFValidationFailuresTotal))
		})
	}
}

func TestValidateBPMN_CallPinsAVersionThatIsNotPublished_Returns200NamingTheCall(t *testing.T) {
	t.Parallel()
	draftNumberless := &domain.ModuleVersion{
		ID: uuid.New(), ModuleID: uuid.New(), ProcessID: "Process_Review", Status: domain.VersionStatusDraft, BPMNXML: "<draft/>",
	}
	cases := []struct {
		name   string
		lookup func() (*domain.ModuleVersion, error)
	}{
		{"no such version of the module", func() (*domain.ModuleVersion, error) { return nil, domain.ErrNotFound }},
		{"the version found has no number because it was never published", func() (*domain.ModuleVersion, error) {
			return draftNumberless, nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wire := w9bNewValidateWire(t, bpmn_compiler.New())
			wire.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Review", int32(99)).DoAndReturn(
				func(context.Context, uuid.UUID, string, int32) (*domain.ModuleVersion, error) { return tc.lookup() })

			rec := do(wire.router, w9bValidateReq(w9bCallerXML("Process_Main", w9bCallSpec{id: "CA_Review", process: "Process_Review", pin: "99"})))

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			body := w9bDecodeValidate(t, rec)
			assert.False(t, body.IsValid)
			require.Len(t, body.Issues, 1)
			assert.Equal(t, string(domain.BPMNErrUnresolvedCalledElement), body.Issues[0].Code)
			assert.Equal(t, "CA_Review", body.Issues[0].NodeID)
			assert.Equal(t, string(domain.SeverityError), body.Issues[0].Severity)
			assert.Contains(t, body.Issues[0].Message, `"Process_Review" version 99`)
		})
	}
}

func TestValidateBPMN_CallsToResolvablePublishedModules_Returns200ValidAndFetchesEachModuleOnce(t *testing.T) {
	t.Parallel()
	wire := w9bNewValidateWire(t, nil)
	review3 := w9bPublishedModule("Process_Review", 3, "<review3/>")
	review4 := w9bPublishedModule("Process_Review", 4, "<review4/>")
	check1 := w9bPublishedModule("Process_Check", 1, "<check1/>")
	calls := []domain.ModuleCall{
		{CallID: "CA_1", ProcessID: "Process_Review", VersionNumber: 3},
		{CallID: "CA_2", ProcessID: "Process_Review"},
		{CallID: "CA_3", ProcessID: "Process_Review"},
	}
	wire.compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return(calls, nil, nil)
	wire.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Review", int32(3)).Return(review3, nil)
	wire.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Review", int32(0)).Return(review4, nil)
	wire.compiler.EXPECT().ModuleCalls(gomock.Any(), "<review3/>").
		Return([]domain.ModuleCall{{CallID: "Inner_1", ProcessID: "Process_Check"}}, nil, nil)
	wire.compiler.EXPECT().ModuleCalls(gomock.Any(), "<review4/>").
		Return([]domain.ModuleCall{{CallID: "Inner_2", ProcessID: "Process_Check"}}, nil, nil)
	wire.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Check", int32(0)).Return(check1, nil)
	wire.compiler.EXPECT().ModuleCalls(gomock.Any(), "<check1/>").Return(nil, nil, nil)
	var bundledWith []domain.ResolvedModule
	wire.compiler.EXPECT().BundleModules("<main/>", gomock.Any()).DoAndReturn(
		func(_ string, modules []domain.ResolvedModule) (string, error) {
			bundledWith = modules
			return "<bundled/>", nil
		})
	wire.compiler.EXPECT().ValidateForPublish(gomock.Any(), "<bundled/>").Return(nil, nil)

	rec := do(wire.router, w9bValidateReq("<main/>"))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := w9bDecodeValidate(t, rec)
	assert.True(t, body.IsValid)
	assert.NotNil(t, body.Issues, "issues is an empty list, not null")
	assert.Empty(t, body.Issues)
	assert.Equal(t, []domain.ResolvedModule{
		{ProcessID: "Process_Review", VersionNumber: 3, PlanName: "Process_Review@v3", BPMNXML: "<review3/>"},
		{ProcessID: "Process_Review", VersionNumber: 0, PlanName: "Process_Review@v4", BPMNXML: "<review4/>"},
		{ProcessID: "Process_Check", VersionNumber: 0, PlanName: "Process_Check@v1", BPMNXML: "<check1/>"},
	}, bundledWith, "the main document and the modules' own calls are walked and every module is bundled once")
}

func TestValidateBPMN_ModuleLookupFindsDatabaseUnavailable_Returns503WithoutRetry(t *testing.T) {
	t.Parallel()
	outage := fmt.Errorf("resolve module: %w: connection refused", domain.ErrUpstreamUnavailable)
	cases := []struct {
		name  string
		setup func(w *w9bValidateWire)
	}{
		{"the lookup of a module the document calls", func(w *w9bValidateWire) {
			w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return([]domain.ModuleCall{
				{CallID: "CA_1", ProcessID: "Process_Review"}, {CallID: "CA_2", ProcessID: "Process_Check"},
			}, nil, nil)
			w.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Review", int32(0)).Return(nil, outage)
		}},
		{"the lookup of a module another module calls", func(w *w9bValidateWire) {
			w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").
				Return([]domain.ModuleCall{{CallID: "CA_1", ProcessID: "Process_Review"}}, nil, nil)
			w.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Review", int32(0)).
				Return(w9bPublishedModule("Process_Review", 1, "<review/>"), nil)
			w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<review/>").
				Return([]domain.ModuleCall{{CallID: "Inner_1", ProcessID: "Process_Check"}}, nil, nil)
			w.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Check", int32(0)).Return(nil, outage)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wire := w9bNewValidateWire(t, nil)
			tc.setup(wire)

			rec := do(wire.router, w9bValidateReq("<main/>"))

			prob := w8cAssertProblem(t, rec, http.StatusServiceUnavailable, w9bTypeUnavail, "UPSTREAM_UNAVAILABLE",
				"an upstream dependency is temporarily unavailable", w9bValidatePath)
			assert.NotContains(t, prob, "is_valid", "an outage is a problem, not a validation answer")
			assert.NotContains(t, rec.Body.String(), "connection refused", "the cause stays out of the body")
			logged := wire.log.errorLines("request failed")
			require.Len(t, logged, 1)
			assert.EqualValues(t, http.StatusServiceUnavailable, logged[0].fields["status"])
			assert.Equal(t, "UPSTREAM_UNAVAILABLE", logged[0].fields["code"])
			assert.Equal(t, testTenantID.String(), logged[0].fields["tenant_id"])
		})
	}
}

func TestValidateBPMN_FailureBehindTheRequest_Returns500WithoutLeakingTheCause(t *testing.T) {
	t.Parallel()
	const leaked = "zzq_secret_cause"
	oneCall := []domain.ModuleCall{{CallID: "CA_1", ProcessID: "Process_Review"}}
	cases := []struct {
		name  string
		setup func(w *w9bValidateWire)
	}{
		{"a module lookup fails with an error that is not an outage", func(w *w9bValidateWire) {
			w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return(oneCall, nil, nil)
			w.modules.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Review", int32(0)).Return(nil, errors.New(leaked))
		}},
		{"listing the module calls fails", func(w *w9bValidateWire) {
			w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return(nil, nil, errors.New(leaked))
		}},
		{"bundling the modules fails", func(w *w9bValidateWire) {
			w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return(nil, nil, nil)
			w.compiler.EXPECT().BundleModules("<main/>", []domain.ResolvedModule(nil)).Return("", errors.New(leaked))
		}},
		{"the validator itself fails", func(w *w9bValidateWire) {
			w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<main/>").Return(nil, nil, nil)
			w.compiler.EXPECT().BundleModules("<main/>", []domain.ResolvedModule(nil)).Return("<bundled/>", nil)
			w.compiler.EXPECT().ValidateForPublish(gomock.Any(), "<bundled/>").Return(nil, errors.New(leaked))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wire := w9bNewValidateWire(t, nil)
			tc.setup(wire)

			rec := do(wire.router, w9bValidateReq("<main/>"))

			prob := w8cAssertProblem(t, rec, http.StatusInternalServerError, w9bTypeInternal, "INTERNAL_ERROR",
				"an unexpected error occurred", w9bValidatePath)
			assert.NotContains(t, prob, "is_valid")
			assert.NotContains(t, rec.Body.String(), leaked, "the raw error text is not sent to the caller")
			logged := wire.log.errorLines("request failed")
			require.Len(t, logged, 1)
			assert.EqualValues(t, http.StatusInternalServerError, logged[0].fields["status"])
			assert.Equal(t, "INTERNAL_ERROR", logged[0].fields["code"])
			assert.Equal(t, testTenantID.String(), logged[0].fields["tenant_id"])
			assert.Equal(t, w9bValidatePath, logged[0].fields["path"])
			assert.Contains(t, logged[0].fields["error"], leaked, "the cause is logged for the operator")
		})
	}
}
