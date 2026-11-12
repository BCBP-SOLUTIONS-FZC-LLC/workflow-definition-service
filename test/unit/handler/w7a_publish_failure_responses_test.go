package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

const (
	w7aDraftXML    = "<w7a-draft/>"
	w7aActiveXML   = "<w7a-active/>"
	w7aArtifact    = "w7a-artifact-hash"
	w7aProblemBase = "https://api.workflow.platform/errors/"
	w7aGenericText = "an unexpected error occurred"
)

type w7aLog struct {
	testkit.FakeLogger
	mu     sync.Mutex
	infos  []string
	errors []map[string]any
}

func (l *w7aLog) Info(msg string, _ map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infos = append(l.infos, msg)
}

func (l *w7aLog) Error(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := map[string]any{"message": msg}
	for k, v := range fields {
		entry[k] = v
	}
	l.errors = append(l.errors, entry)
}

func (l *w7aLog) loggedErrors() []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]map[string]any(nil), l.errors...)
}

func (l *w7aLog) publishedLines() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, m := range l.infos {
		if m == "version published" {
			n++
		}
	}
	return n
}

type w7aRig struct {
	versions   *mocks.MockWorkflowVersionRepository
	workflows  *mocks.MockWorkflowRepository
	assignees  *mocks.MockAssigneeRepository
	compiler   *mocks.MockPlanCompiler
	tx         *mocks.MockTransactor
	membership *mocks.MockMembershipService
	log        *w7aLog
	router     *gin.Engine
	txResults  []error
}

func w7aNewRig(t *testing.T, withMembership bool) *w7aRig {
	t.Helper()
	ctrl := gomock.NewController(t)
	rig := &w7aRig{
		versions:  mocks.NewMockWorkflowVersionRepository(ctrl),
		workflows: mocks.NewMockWorkflowRepository(ctrl),
		assignees: mocks.NewMockAssigneeRepository(ctrl),
		compiler:  mocks.NewMockPlanCompiler(ctrl),
		tx:        mocks.NewMockTransactor(ctrl),
		log:       &w7aLog{},
	}
	deps := service.VersionDeps{
		Transactor: rig.tx, Workflows: rig.workflows, Versions: rig.versions,
		Assignees: rig.assignees, Compiler: rig.compiler, Log: rig.log,
	}
	if withMembership {
		rig.membership = mocks.NewMockMembershipService(ctrl)
		deps.Membership = rig.membership
	}
	rig.router = newRouter(handler.New(handler.Services{Versions: service.NewVersionService(deps), Log: rig.log}))
	return rig
}

func w7aPublishPath() string {
	return "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
}

func w7aCollab(plan *dsl.CompiledPlan) *dsl.CompiledCollaboration {
	return &dsl.CompiledCollaboration{MainPlan: plan.Name, Plans: []*dsl.CompiledPlan{plan}, SchemaVersion: dsl.CurrentSchemaVersion}
}

func w7aDraft() *domain.WorkflowVersion {
	return &domain.WorkflowVersion{
		ID: testVerID, WorkflowID: testWFID, TenantID: testTenantID,
		Status: domain.VersionStatusDraft, BPMNXML: w7aDraftXML, RecordVersion: 4,
	}
}

func (r *w7aRig) expectDraftCompiles(collab *dsl.CompiledCollaboration) {
	r.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(w7aDraft(), nil)
	r.compiler.EXPECT().ModuleCalls(gomock.Any(), w7aDraftXML).Return(nil, nil, nil)
	r.compiler.EXPECT().BundleModules(w7aDraftXML, gomock.Len(0)).Return(w7aDraftXML, nil)
	r.compiler.EXPECT().CompileForPublish(gomock.Any(), w7aDraftXML).Return(collab, nil)
}

func (r *w7aRig) expectHashAndNoActiveVersion() {
	r.compiler.EXPECT().Hash(gomock.Any(), w7aDraftXML).Return(w7aArtifact, nil)
	r.workflows.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(&domain.Workflow{ID: testWFID}, nil)
}

func (r *w7aRig) expectTransaction() {
	r.tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error {
			err := fn(ctx)
			r.txResults = append(r.txResults, err)
			return err
		})
}

func w7aPublish(rig *w7aRig) (*http.Request, int, map[string]any, string) {
	r := adminReq(http.MethodPost, w7aPublishPath(), nil)
	w := do(rig.router, r)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return r, w.Code, body, w.Header().Get("Content-Type")
}

func w7aAssertProblem(t *testing.T, status int, body map[string]any, contentType string, wantStatus int, wantType, wantCode string) {
	t.Helper()
	require.Equal(t, wantStatus, status, "%v", body)
	assert.Equal(t, "application/problem+json", contentType)
	assert.Equal(t, w7aProblemBase+wantType, body["type"])
	assert.Equal(t, wantCode, body["code"])
	assert.EqualValues(t, wantStatus, body["status"])
	assert.Equal(t, w7aPublishPath(), body["instance"])
}

func w7aStagePlan(name, dept string, assignees ...string) *dsl.CompiledPlan {
	return &dsl.CompiledPlan{Name: name, Departments: []dsl.DepartmentDef{{
		ID: dept, Label: dept, IAMDepartmentID: uuid.NewString(),
		Stages: []dsl.StageDef{{Type: "review", NodeID: "w7a-task", Role: "reviewer", DefaultAssignees: assignees}},
	}}}
}

func TestPublishVersion_CollaborationWhoseMainPlanIsNotAmongItsPlans_Returns500AndWritesNothing(t *testing.T) {
	t.Parallel()

	rig := w7aNewRig(t, false)
	collab := w7aCollab(&dsl.CompiledPlan{Name: "w7a-compiled", Departments: []dsl.DepartmentDef{{ID: "w7a-dept", Label: "w7a-dept"}}})
	collab.MainPlan = "w7a-absent"
	rig.expectDraftCompiles(collab)

	_, status, body, contentType := w7aPublish(rig)

	w7aAssertProblem(t, status, body, contentType, http.StatusInternalServerError, "internal-error", "INTERNAL_ERROR")
	assert.Equal(t, w7aGenericText, body["detail"])
	assert.NotContains(t, fmt.Sprint(body), "w7a-absent")
	logged := rig.log.loggedErrors()
	require.Len(t, logged, 1)
	assert.Equal(t, "request failed", logged[0]["message"])
	assert.EqualValues(t, http.StatusInternalServerError, logged[0]["status"])
	assert.Contains(t, logged[0]["error"], "compile bpmn")
	assert.Contains(t, logged[0]["error"], "w7a-absent")
	assert.Zero(t, rig.log.publishedLines())
}

func TestPublishVersion_DefaultAssigneeThatIsNotAUUIDv7_Returns422AssigneeIneligibleAndWritesNothing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		withMembership bool
		assignee       string
	}{
		{name: "not a uuid, membership service configured", withMembership: true, assignee: "w7a-not-a-uuid"},
		{name: "a v4 uuid, not v7, membership service configured", withMembership: true, assignee: uuid.NewString()},
		{name: "not a uuid, no membership service", withMembership: false, assignee: "w7a-not-a-uuid"},
		{name: "a v4 uuid, not v7, no membership service", withMembership: false, assignee: uuid.NewString()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rig := w7aNewRig(t, tc.withMembership)
			rig.expectDraftCompiles(w7aCollab(w7aStagePlan("w7a-main", "w7a-dept", tc.assignee)))
			rig.expectHashAndNoActiveVersion()

			_, status, body, contentType := w7aPublish(rig)

			w7aAssertProblem(t, status, body, contentType, http.StatusUnprocessableEntity, "validation-failed", "ASSIGNEE_INELIGIBLE")
			assert.Contains(t, body["detail"], "invalid assignee UUID "+tc.assignee)
			assert.Zero(t, rig.log.publishedLines())
			assert.Empty(t, rig.log.loggedErrors(), "a refusal is not a server error")
		})
	}
}

func TestPublishVersion_TwoAssigneeChecksWhereOneIsRefusedAndOneFailsUpstream_AnswersForTheFirstInPlanOrder(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		firstRefused bool
		wantStatus   int
		wantType     string
		wantCode     string
	}{
		{name: "first refused, second upstream failure", firstRefused: true, wantStatus: http.StatusUnprocessableEntity, wantType: "validation-failed", wantCode: "ASSIGNEE_INELIGIBLE"},
		{name: "first upstream failure, second refused", firstRefused: false, wantStatus: http.StatusServiceUnavailable, wantType: "service-unavailable", wantCode: "UPSTREAM_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			first, second := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			plan := &dsl.CompiledPlan{Name: "w7a-main", Departments: []dsl.DepartmentDef{
				{ID: "w7a-first", IAMDepartmentID: "w7a-iam-first", Stages: []dsl.StageDef{{Type: "review", Role: "reviewer", DefaultAssignees: []string{first.String()}}}},
				{ID: "w7a-second", IAMDepartmentID: "w7a-iam-second", Stages: []dsl.StageDef{{Type: "review", Role: "reviewer", DefaultAssignees: []string{second.String()}}}},
			}}
			rig := w7aNewRig(t, true)
			rig.expectDraftCompiles(w7aCollab(plan))
			rig.expectHashAndNoActiveVersion()
			refused := func() (domain.Eligibility, error) {
				return domain.Eligibility{Reason: "w7a-not-in-department"}, nil
			}
			failed := func() (domain.Eligibility, error) {
				return domain.Eligibility{}, fmt.Errorf("%w: membership returned 503", domain.ErrUpstreamUnavailable)
			}
			firstOutcome, secondOutcome := failed, refused
			if tc.firstRefused {
				firstOutcome, secondOutcome = refused, failed
			}
			rig.membership.EXPECT().CheckEligibility(gomock.Any(), testTenantID, first, "w7a-iam-first", "reviewer").
				DoAndReturn(func(context.Context, uuid.UUID, uuid.UUID, string, string) (domain.Eligibility, error) {
					return firstOutcome()
				}).Times(1)
			rig.membership.EXPECT().CheckEligibility(gomock.Any(), testTenantID, second, "w7a-iam-second", "reviewer").
				DoAndReturn(func(context.Context, uuid.UUID, uuid.UUID, string, string) (domain.Eligibility, error) {
					return secondOutcome()
				}).Times(1)

			_, status, body, contentType := w7aPublish(rig)

			w7aAssertProblem(t, status, body, contentType, tc.wantStatus, tc.wantType, tc.wantCode)
			assert.Zero(t, rig.log.publishedLines())
		})
	}
}

func TestPublishVersion_ActiveVersionThatCannotBeCompiledForANonValidationReason_Returns500AndWritesNothing(t *testing.T) {
	t.Parallel()

	const cause = "w7a parser crashed"
	activeID := uuid.New()
	rig := w7aNewRig(t, false)
	rig.expectDraftCompiles(w7aCollab(w7aStagePlan("w7a-main", "w7a-dept")))
	rig.compiler.EXPECT().Hash(gomock.Any(), w7aDraftXML).Return(w7aArtifact, nil)
	rig.workflows.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(&domain.Workflow{ID: testWFID, ActiveVersionID: &activeID}, nil)
	rig.versions.EXPECT().GetByID(gomock.Any(), testTenantID, activeID).Return(&domain.WorkflowVersion{
		ID: activeID, WorkflowID: testWFID, TenantID: testTenantID, Status: domain.VersionStatusPublished, BPMNXML: w7aActiveXML,
	}, nil)
	rig.compiler.EXPECT().ModuleCalls(gomock.Any(), w7aActiveXML).Return(nil, nil, errors.New(cause))

	_, status, body, contentType := w7aPublish(rig)

	w7aAssertProblem(t, status, body, contentType, http.StatusInternalServerError, "internal-error", "INTERNAL_ERROR")
	assert.Equal(t, w7aGenericText, body["detail"])
	assert.NotContains(t, fmt.Sprint(body), cause)
	logged := rig.log.loggedErrors()
	require.Len(t, logged, 1)
	assert.Contains(t, logged[0]["error"], "compile active version for divergence check")
	assert.Contains(t, logged[0]["error"], cause)
	assert.Zero(t, rig.log.publishedLines())
}

func TestPublishVersion_AWriteInsideTheTransactionFails_Returns500RollsBackAndRunsNoLaterWrite(t *testing.T) {
	t.Parallel()

	const driverText = "w7a write refused: relation is locked"
	assignee := uuid.Must(uuid.NewV7())
	cases := []struct {
		name  string
		stage string
	}{
		{name: "the version row is not published"},
		{name: "the old assignees are not deleted", stage: "delete"},
		{name: "the new assignees are not inserted", stage: "insert"},
		{name: "the workflow's active version is not moved", stage: "activate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			boom := errors.New(driverText)
			rig := w7aNewRig(t, false)
			plan := w7aStagePlan("w7a-main", "w7a-dept", assignee.String())
			rig.expectDraftCompiles(w7aCollab(plan))
			rig.expectHashAndNoActiveVersion()
			rig.expectTransaction()
			rig.versions.EXPECT().NextVersionNumber(gomock.Any(), testTenantID, testWFID).Return(int32(6), nil)
			publishResult := error(nil)
			if tc.stage == "" {
				publishResult = boom
			}
			rig.versions.EXPECT().Publish(gomock.Any(), testTenantID, testVerID, int64(4), int32(6), gomock.Not(gomock.Eq("")), w7aArtifact, gomock.Len(0)).Return(publishResult)
			if tc.stage != "" {
				deleteResult := error(nil)
				if tc.stage == "delete" {
					deleteResult = boom
				}
				rig.assignees.EXPECT().DeleteByVersion(gomock.Any(), testTenantID, testVerID).Return(deleteResult)
			}
			if tc.stage == "insert" || tc.stage == "activate" {
				insertResult := error(nil)
				if tc.stage == "insert" {
					insertResult = boom
				}
				rig.assignees.EXPECT().BulkInsert(gomock.Any(), testTenantID, testVerID, gomock.Any()).
					DoAndReturn(func(_ context.Context, _, _ uuid.UUID, got []*domain.NodeAssignee) error {
						require.Len(t, got, 1)
						assert.Equal(t, assignee, got[0].UserID)
						assert.Equal(t, "w7a-dept/w7a-task", got[0].NodeKey)
						return insertResult
					})
			}
			if tc.stage == "activate" {
				rig.workflows.EXPECT().UpdateActiveVersion(gomock.Any(), testTenantID, testWFID, gomock.Eq(&testVerID)).Return(boom)
			}

			_, status, body, contentType := w7aPublish(rig)

			w7aAssertProblem(t, status, body, contentType, http.StatusInternalServerError, "internal-error", "INTERNAL_ERROR")
			assert.Equal(t, w7aGenericText, body["detail"])
			assert.NotContains(t, fmt.Sprint(body), driverText)
			require.Len(t, rig.txResults, 1)
			assert.ErrorIs(t, rig.txResults[0], boom, "the transaction function returned the failure, so the transactor rolls back")
			assert.Zero(t, rig.log.publishedLines())
			logged := rig.log.loggedErrors()
			require.Len(t, logged, 1)
			assert.Contains(t, logged[0]["error"], driverText)
		})
	}
}

func TestPublishVersion_HashOfTheBundledBPMNFails_Returns500AndStartsNoTransaction(t *testing.T) {
	t.Parallel()

	const cause = "w7a canonical form failed"
	rig := w7aNewRig(t, false)
	rig.expectDraftCompiles(w7aCollab(w7aStagePlan("w7a-main", "w7a-dept")))
	rig.compiler.EXPECT().Hash(gomock.Any(), w7aDraftXML).Return("", errors.New(cause))

	_, status, body, contentType := w7aPublish(rig)

	w7aAssertProblem(t, status, body, contentType, http.StatusInternalServerError, "internal-error", "INTERNAL_ERROR")
	assert.Equal(t, w7aGenericText, body["detail"])
	assert.NotContains(t, fmt.Sprint(body), cause)
	logged := rig.log.loggedErrors()
	require.Len(t, logged, 1)
	assert.Contains(t, logged[0]["error"], "hash bpmn")
	assert.Contains(t, logged[0]["error"], cause)
	assert.Empty(t, rig.txResults)
	assert.Zero(t, rig.log.publishedLines())
}

func TestPublishVersion_NextVersionNumberCannotBeRead_Returns500ForAnUnknownFailureAnd503ForAnOutage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		failure    error
		wantStatus int
		wantType   string
		wantCode   string
		wantDetail string
	}{
		{
			name: "unknown failure", failure: errors.New("w7a sequence broken"), wantStatus: http.StatusInternalServerError,
			wantType: "internal-error", wantCode: "INTERNAL_ERROR", wantDetail: w7aGenericText,
		},
		{
			name: "connection error", failure: fmt.Errorf("%w: dial tcp: connection refused", domain.ErrUpstreamUnavailable), wantStatus: http.StatusServiceUnavailable,
			wantType: "service-unavailable", wantCode: "UPSTREAM_UNAVAILABLE", wantDetail: "an upstream dependency is temporarily unavailable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rig := w7aNewRig(t, false)
			rig.expectDraftCompiles(w7aCollab(w7aStagePlan("w7a-main", "w7a-dept")))
			rig.expectHashAndNoActiveVersion()
			rig.expectTransaction()
			rig.versions.EXPECT().NextVersionNumber(gomock.Any(), testTenantID, testWFID).Return(int32(0), tc.failure)

			_, status, body, contentType := w7aPublish(rig)

			w7aAssertProblem(t, status, body, contentType, tc.wantStatus, tc.wantType, tc.wantCode)
			assert.Equal(t, tc.wantDetail, body["detail"])
			require.Len(t, rig.txResults, 1)
			assert.ErrorIs(t, rig.txResults[0], tc.failure)
			assert.Zero(t, rig.log.publishedLines())
		})
	}
}

func TestPublishVersion_DraftThatDiffersFromTheActiveVersionOnlyInLabelsAndNames_PublishesWithoutForce(t *testing.T) {
	t.Parallel()

	stage := func(name string) dsl.StageDef {
		return dsl.StageDef{Type: "review", NodeID: "w7a-task", Name: name, Role: "reviewer"}
	}
	activePlan := &dsl.CompiledPlan{Name: "w7a-main", Departments: []dsl.DepartmentDef{
		{ID: "w7a-dept", Label: "w7a-accounts", Stages: []dsl.StageDef{stage("w7a-check-invoice")}},
	}}
	draftPlan := &dsl.CompiledPlan{Name: "w7a-main", Departments: []dsl.DepartmentDef{
		{ID: "w7a-dept", Label: "w7a-finance", Stages: []dsl.StageDef{stage("w7a-verify-invoice")}},
	}}
	activeJSON, err := json.Marshal(w7aCollab(activePlan))
	require.NoError(t, err)
	activeStored := string(activeJSON)
	activeID := uuid.New()
	activeNumber := int32(2)

	rig := w7aNewRig(t, false)
	rig.expectDraftCompiles(w7aCollab(draftPlan))
	rig.compiler.EXPECT().Hash(gomock.Any(), w7aDraftXML).Return(w7aArtifact, nil)
	rig.workflows.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(&domain.Workflow{ID: testWFID, ActiveVersionID: &activeID}, nil)
	rig.versions.EXPECT().GetByID(gomock.Any(), testTenantID, activeID).Return(&domain.WorkflowVersion{
		ID: activeID, WorkflowID: testWFID, TenantID: testTenantID, Status: domain.VersionStatusPublished,
		VersionNumber: &activeNumber, CompiledPlanJSON: &activeStored,
	}, nil)
	rig.expectTransaction()
	rig.versions.EXPECT().NextVersionNumber(gomock.Any(), testTenantID, testWFID).Return(int32(3), nil)
	var storedPlan string
	rig.versions.EXPECT().Publish(gomock.Any(), testTenantID, testVerID, int64(4), int32(3), gomock.Any(), w7aArtifact, gomock.Len(0)).
		DoAndReturn(func(_ context.Context, _, _ uuid.UUID, _ int64, _ int32, compiled, _ string, _ []domain.ModuleRef) error {
			storedPlan = compiled
			return nil
		})
	rig.assignees.EXPECT().DeleteByVersion(gomock.Any(), testTenantID, testVerID).Return(nil)
	rig.workflows.EXPECT().UpdateActiveVersion(gomock.Any(), testTenantID, testWFID, gomock.Eq(&testVerID)).Return(nil)

	_, status, body, _ := w7aPublish(rig)

	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, testWFID.String(), body["workflow_id"])
	assert.Equal(t, testVerID.String(), body["version_id"])
	assert.Equal(t, "PUBLISHED", body["status"])
	assert.EqualValues(t, 3, body["version_number"])
	assert.Equal(t, "Version published", body["message"])
	assert.Contains(t, storedPlan, "w7a-finance", "the draft's labels are what gets stored")
	assert.Contains(t, storedPlan, "w7a-verify-invoice")
	assert.NotContains(t, storedPlan, "w7a-accounts")
	assert.Equal(t, 1, rig.log.publishedLines())
	assert.Equal(t, []error{nil}, rig.txResults)
}
