package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

func w3cRegisterMetrics(t *testing.T) {
	t.Helper()
	log, err := logger.NewLogger("dev")
	require.NoError(t, err)
	_ = gincommon.ObservabilityMiddlewares(gincommon.Config{
		Logger: log, ServiceName: "workflow-definition-service", BuildVersion: "test",
		Domain: observability.Domain, Environment: "test",
	})
	observability.Register()
}

type w3cOutcomeCounts struct {
	okBefore, errBefore float64
	read                func(label string) float64
}

func w3cWatch(read func(label string) float64) *w3cOutcomeCounts {
	return &w3cOutcomeCounts{okBefore: read("ok"), errBefore: read("err"), read: read}
}

func (c *w3cOutcomeCounts) assertDelta(t *testing.T, ok, failed float64) {
	t.Helper()
	assert.Equal(t, c.okBefore+ok, c.read("ok"), "ok counter")
	assert.Equal(t, c.errBefore+failed, c.read("err"), "err counter")
}

func w3cSubmissions(label string) float64 {
	return testutil.ToFloat64(observability.WFSubmissionsTotal.WithLabelValues(label))
}

func w3cArchives(label string) float64 {
	return testutil.ToFloat64(observability.WFArchiveTotal.WithLabelValues(label))
}

func TestWorkflowService_List_RepositoryFailure_ReturnsWrappedErrorWithNothingElse(t *testing.T) {
	cause := errors.New("db down")
	tenantID := uuid.New()
	search, templateKey := "needle", "tpl-key"
	filter := port.WorkflowFilter{Search: &search, TemplateKey: &templateKey, Page: 3, Limit: 17}

	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	var gotTenant uuid.UUID
	var gotFilter port.WorkflowFilter
	wfRepo.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tid uuid.UUID, f port.WorkflowFilter) ([]*domain.Workflow, int64, error) {
			gotTenant, gotFilter = tid, f
			return []*domain.Workflow{{ID: uuid.New()}}, 9, cause
		})
	svc := service.NewWorkflowService(service.WorkflowDeps{Workflows: wfRepo})

	got, count, err := svc.List(context.Background(), tenantID, filter)

	require.ErrorIs(t, err, cause)
	assert.EqualError(t, err, "list workflows: db down")
	assert.Nil(t, got, "rows the repository returned beside its error must not leak")
	assert.Zero(t, count)
	assert.Equal(t, tenantID, gotTenant)
	assert.Equal(t, filter, gotFilter)
}

func TestWorkflowService_Get_VersionListFailure_ReturnsWrappedErrorAndNoWorkflow(t *testing.T) {
	cause := errors.New("list down")
	tenantID, wfID := uuid.New(), uuid.New()

	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).Return(&domain.Workflow{ID: wfID, TenantID: tenantID}, nil)
	var gotTenant, gotWF uuid.UUID
	var gotPage, gotLimit int
	vRepo.EXPECT().ListByWorkflow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tid, wid uuid.UUID, page, limit int) ([]*domain.WorkflowVersion, int64, error) {
			gotTenant, gotWF, gotPage, gotLimit = tid, wid, page, limit
			return []*domain.WorkflowVersion{{ID: uuid.New()}}, 3, cause
		})
	svc := service.NewWorkflowService(service.WorkflowDeps{Workflows: wfRepo, Versions: vRepo})

	wf, versions, err := svc.Get(context.Background(), tenantID, wfID, 7)

	require.ErrorIs(t, err, cause)
	assert.EqualError(t, err, "list versions: list down")
	assert.Nil(t, wf, "the workflow that was found must not be returned beside the failure")
	assert.Nil(t, versions)
	assert.Equal(t, tenantID, gotTenant)
	assert.Equal(t, wfID, gotWF)
	assert.Equal(t, 1, gotPage)
	assert.Equal(t, 7, gotLimit)
}

func TestWorkflowService_Create_StoresWorkflowAndDraftVersionForTheTenant(t *testing.T) {
	w3cRegisterMetrics(t)
	tenantID, userID := uuid.New(), uuid.New()
	const bpmn = "<bpmn>stored verbatim</bpmn>"

	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	var storedWF *domain.Workflow
	var storedV *domain.WorkflowVersion
	var countTenant uuid.UUID
	gomock.InOrder(
		wfRepo.EXPECT().CountByTenant(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, tid uuid.UUID) (int64, error) { countTenant = tid; return 2, nil }),
		wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, wf *domain.Workflow) error { storedWF = wf; return nil }),
		vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, v *domain.WorkflowVersion) error { storedV = v; return nil }),
	)
	svc := service.NewWorkflowService(service.WorkflowDeps{
		PlanLimits: stdPlanLimits(), Transactor: txPassthrough(ctrl), Workflows: wfRepo, Versions: vRepo,
	})
	watch := w3cWatch(w3cSubmissions)

	wf, v, err := svc.Create(context.Background(), tenantID, userID, "tpl-key", "Template", "About it", bpmn, "starter")

	require.NoError(t, err)
	require.NotNil(t, storedWF)
	require.NotNil(t, storedV)
	assert.Same(t, storedWF, wf, "the workflow returned is the workflow stored")
	assert.Same(t, storedV, v, "the version returned is the version stored")
	assert.Equal(t, tenantID, countTenant)

	assert.NotEqual(t, uuid.Nil, wf.ID)
	assert.NotEqual(t, uuid.Nil, v.ID)
	assert.NotEqual(t, wf.ID, v.ID)
	assert.Equal(t, tenantID, wf.TenantID)
	assert.Equal(t, userID, wf.CreatedByUserID)
	assert.Equal(t, "tpl-key", wf.TemplateKey)
	assert.Equal(t, "Template", wf.Name)
	assert.Equal(t, "About it", wf.Description)
	assert.Nil(t, wf.ActiveVersionID, "a new workflow is not active until a version is promoted")

	assert.Equal(t, wf.ID, v.WorkflowID)
	assert.Equal(t, tenantID, v.TenantID)
	assert.Equal(t, userID, v.CreatedByUserID)
	assert.Equal(t, domain.VersionStatusDraft, v.Status)
	assert.True(t, v.IsValid)
	assert.Equal(t, bpmn, v.BPMNXML)
	assert.Nil(t, v.VersionNumber, "a draft has no version number")
	watch.assertDelta(t, 1, 0)
}

func TestWorkflowService_Create_CompilerValidateError_IsReturnedAndNothingIsPersisted(t *testing.T) {
	w3cRegisterMetrics(t)
	cause := errors.New("compiler exploded")
	tenantID := uuid.New()

	ctrl := gomock.NewController(t)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	expectNoLibraryCalls(compiler)
	var validated string
	compiler.EXPECT().Validate(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, bpmnXML string) ([]domain.BPMNValidationError, error) {
			validated = bpmnXML
			return nil, cause
		})
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Compiler:   compiler,
		Transactor: mocks.NewMockTransactor(ctrl),
		Workflows:  mocks.NewMockWorkflowRepository(ctrl),
		Versions:   mocks.NewMockWorkflowVersionRepository(ctrl),
	})
	watch := w3cWatch(w3cSubmissions)

	wf, v, err := svc.Create(context.Background(), tenantID, uuid.New(), "k", "n", "", "<bpmn/>", "starter")

	require.ErrorIs(t, err, cause)
	assert.EqualError(t, err, "validate bpmn: compiler exploded")
	var findings *domain.ValidationFailedError
	assert.False(t, errors.As(err, &findings), "a failure of the check is not a finding about the document")
	assert.Nil(t, wf)
	assert.Nil(t, v)
	assert.Equal(t, "<bpmn/>", validated)
	watch.assertDelta(t, 0, 1)
}

func TestWorkflowService_Create_CountFailure_IsWrappedTwiceAndInsertsNothing(t *testing.T) {
	w3cRegisterMetrics(t)
	cause := errors.New("count down")
	tenantID := uuid.New()

	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	var countTenant uuid.UUID
	wfRepo.EXPECT().CountByTenant(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tid uuid.UUID) (int64, error) { countTenant = tid; return 0, cause })
	svc := service.NewWorkflowService(service.WorkflowDeps{
		PlanLimits: stdPlanLimits(), Transactor: txPassthrough(ctrl), Workflows: wfRepo, Versions: vRepo,
	})
	watch := w3cWatch(w3cSubmissions)

	wf, v, err := svc.Create(context.Background(), tenantID, uuid.New(), "k", "n", "", "<bpmn/>", "starter")

	require.ErrorIs(t, err, cause)
	assert.EqualError(t, err, "create workflow: count workflows: count down")
	assert.NotErrorIs(t, err, domain.ErrPlanQuotaExceeded, "a failed count is not a quota refusal")
	assert.Nil(t, wf)
	assert.Nil(t, v)
	assert.Equal(t, tenantID, countTenant)
	watch.assertDelta(t, 0, 1)
}

func TestWorkflowService_Create_QuotaBoundary_RefusesAtTheLimitWithoutInserting(t *testing.T) {
	w3cRegisterMetrics(t)
	cases := []struct {
		name       string
		planCode   string
		limit      int64
		count      int64
		wantStored bool
	}{
		{name: "one below the limit stores", planCode: "starter", limit: 5, count: 4, wantStored: true},
		{name: "at the limit is refused", planCode: "starter", limit: 5, count: 5},
		{name: "above the limit is refused", planCode: "starter", limit: 5, count: 6},
		{name: "a limit of zero refuses an empty tenant", planCode: "frozen", limit: 0, count: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID := uuid.New()
			ctrl := gomock.NewController(t)
			wfRepo := mocks.NewMockWorkflowRepository(ctrl)
			vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
			var countTenant uuid.UUID
			wfRepo.EXPECT().CountByTenant(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, tid uuid.UUID) (int64, error) { countTenant = tid; return tc.count, nil })
			if tc.wantStored {
				wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
				vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
			}
			limits := staticLimits{tc.planCode: limitOf(tc.limit)}
			svc := service.NewWorkflowService(service.WorkflowDeps{
				PlanLimits: limits, Transactor: txPassthrough(ctrl), Workflows: wfRepo, Versions: vRepo,
			})
			watch := w3cWatch(w3cSubmissions)

			wf, v, err := svc.Create(context.Background(), tenantID, uuid.New(), "k", "n", "", "<bpmn/>", tc.planCode)

			assert.Equal(t, tenantID, countTenant)
			if tc.wantStored {
				require.NoError(t, err)
				assert.NotNil(t, wf)
				assert.NotNil(t, v)
				watch.assertDelta(t, 1, 0)
				return
			}
			require.ErrorIs(t, err, domain.ErrPlanQuotaExceeded)
			assert.EqualError(t, err, "create workflow: plan quota exceeded")
			assert.Nil(t, wf)
			assert.Nil(t, v)
			watch.assertDelta(t, 0, 1)
		})
	}
}

func TestWorkflowService_Archive_MissingWorkflow_FailsBeforeAnyOtherCall(t *testing.T) {
	w3cRegisterMetrics(t)
	tenantID, userID, wfID := uuid.New(), uuid.New(), uuid.New()

	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	var gotTenant, gotID uuid.UUID
	wfRepo.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tid, id uuid.UUID) (*domain.Workflow, error) {
			gotTenant, gotID = tid, id
			return nil, domain.ErrNotFound
		})
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Workflows:  wfRepo,
		Versions:   mocks.NewMockWorkflowVersionRepository(ctrl),
		Execution:  mocks.NewMockExecutionService(ctrl),
		Transactor: mocks.NewMockTransactor(ctrl),
		Cache:      mocks.NewMockCacheStore(ctrl),
	})
	watch := w3cWatch(w3cArchives)

	err := svc.Archive(context.Background(), tenantID, userID, wfID)

	require.ErrorIs(t, err, domain.ErrNotFound)
	assert.EqualError(t, err, "get workflow: "+domain.ErrNotFound.Error())
	assert.Equal(t, tenantID, gotTenant, "the lookup is scoped to the caller's tenant")
	assert.Equal(t, wfID, gotID)
	watch.assertDelta(t, 0, 1)
}

func TestWorkflowService_Archive_VersionNotPublished_AbortsBeforeThePointerMoves(t *testing.T) {
	w3cRegisterMetrics(t)
	tenantID, userID, wfID, activeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	wfRepo.EXPECT().GetByID(gomock.Any(), tenantID, wfID).
		Return(&domain.Workflow{ID: wfID, TenantID: tenantID, ActiveVersionID: &activeID}, nil).Times(2)
	var archivedTenant, archivedVersion uuid.UUID
	vRepo.EXPECT().Archive(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tid, vid uuid.UUID) error {
			archivedTenant, archivedVersion = tid, vid
			return domain.ErrVersionNotPublished
		})
	svc := service.NewWorkflowService(service.WorkflowDeps{
		Transactor: txPassthrough(ctrl), Workflows: wfRepo, Versions: vRepo,
		Cache: mocks.NewMockCacheStore(ctrl),
	})
	watch := w3cWatch(w3cArchives)

	err := svc.Archive(context.Background(), tenantID, userID, wfID)

	require.ErrorIs(t, err, domain.ErrVersionNotPublished)
	assert.EqualError(t, err, "archive workflow: "+domain.ErrVersionNotPublished.Error())
	assert.Equal(t, tenantID, archivedTenant)
	assert.Equal(t, activeID, archivedVersion)
	watch.assertDelta(t, 0, 1)
}

const w3cLanelessBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="Def_L" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_Main" name="Main" isExecutable="true">
    <bpmn:startEvent id="Start_1"/>
    <bpmn:userTask id="Task_A"><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparator" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/></bpmn:extensionElements></bpmn:userTask>
    <bpmn:endEvent id="End_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start_1" targetRef="Task_A"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_A" targetRef="End_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="Process_Main">
      <bpmndi:BPMNShape id="Start_1_di" bpmnElement="Start_1"/>
      <bpmndi:BPMNShape id="Task_A_di" bpmnElement="Task_A"/>
      <bpmndi:BPMNShape id="End_1_di" bpmnElement="End_1"/>
      <bpmndi:BPMNEdge id="F1_di" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="F2_di" bpmnElement="F2"/>
  </bpmndi:BPMNPlane></bpmndi:BPMNDiagram>
</bpmn:definitions>`

func TestWorkflowService_Create_AcceptsADocumentTheValidateServiceReportsInvalid(t *testing.T) {
	w3cRegisterMetrics(t)
	tenantID, userID := uuid.New(), uuid.New()
	compiler := bpmn_compiler.New()

	ctrl := gomock.NewController(t)
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	var stored *domain.WorkflowVersion
	wfRepo.EXPECT().CountByTenant(gomock.Any(), tenantID).Return(int64(0), nil)
	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, v *domain.WorkflowVersion) error { stored = v; return nil })
	workflows := service.NewWorkflowService(service.WorkflowDeps{
		Compiler: compiler, PlanLimits: stdPlanLimits(), Transactor: txPassthrough(ctrl),
		Workflows: wfRepo, Versions: vRepo,
	})
	validation := service.NewValidationService(service.ValidationDeps{Compiler: compiler})

	wf, v, err := workflows.Create(context.Background(), tenantID, userID, "laneless", "Laneless", "", w3cLanelessBPMN, "starter")
	require.NoError(t, err, "create accepts a document that only the publish-time checks refuse")
	require.NotNil(t, wf)
	assert.Same(t, stored, v)
	assert.Equal(t, domain.VersionStatusDraft, v.Status)
	assert.Equal(t, w3cLanelessBPMN, v.BPMNXML)

	isValid, findings, err := validation.Validate(context.Background(), tenantID, w3cLanelessBPMN)
	require.NoError(t, err)
	assert.False(t, isValid, "the same document is reported invalid by the validate service")
	var missing *domain.BPMNValidationError
	for i := range findings {
		if findings[i].Code == domain.BPMNErrMissingDeptID {
			missing = &findings[i]
		}
	}
	require.NotNil(t, missing, "findings = %+v", findings)
	assert.Equal(t, "Task_A", missing.NodeID)
	assert.NotEqual(t, domain.SeverityWarning, missing.Severity, fmt.Sprintf("finding = %+v", *missing))
}
