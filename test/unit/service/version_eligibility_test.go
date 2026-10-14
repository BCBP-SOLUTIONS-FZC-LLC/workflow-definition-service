package service_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type eligibilityFake struct {
	mu       sync.Mutex
	calls    map[string]int
	inFlight atomic.Int32
	peak     atomic.Int32
	delay    func(user uuid.UUID) time.Duration
	eligible func(user uuid.UUID) bool
}

func (f *eligibilityFake) CheckEligibility(_ context.Context, _, userID uuid.UUID, dept, role string) (bool, error) {
	n := f.inFlight.Add(1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	defer f.inFlight.Add(-1)
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[userID.String()+"|"+dept+"|"+role]++
	f.mu.Unlock()
	if f.delay != nil {
		time.Sleep(f.delay(userID))
	}
	return f.eligible(userID), nil
}

func publishWithAssignees(t *testing.T, fake *eligibilityFake, plan *dsl.CompiledPlan) error {
	t.Helper()
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler, Membership: fake})

	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(
		&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(plan), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("hash", nil)

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)
	return err
}

func stageOf(role string, users ...uuid.UUID) dsl.StageDef {
	ids := make([]string, len(users))
	for i, u := range users {
		ids[i] = u.String()
	}
	return dsl.StageDef{Type: "review", Role: role, DefaultAssignees: ids}
}

func TestPublish_ChecksEachDistinctAssigneeOnce(t *testing.T) {
	u := uuid.Must(uuid.NewV7())
	plan := &dsl.CompiledPlan{Departments: []dsl.DepartmentDef{
		{ID: "a", IAMDepartmentID: "dept-a", Stages: []dsl.StageDef{stageOf("reviewer", u), stageOf("reviewer", u), stageOf("approver", u)}},
		{ID: "b", IAMDepartmentID: "dept-a", Stages: []dsl.StageDef{stageOf("reviewer", u)}},
	}}
	fake := &eligibilityFake{eligible: func(uuid.UUID) bool { return false }}

	err := publishWithAssignees(t, fake, plan)

	require.ErrorIs(t, err, domain.ErrAssigneeIneligible)
	total := 0
	for key, n := range fake.calls {
		assert.Equal(t, 1, n, "%s was checked more than once", key)
		total += n
	}
	assert.Equal(t, 2, total, "two distinct (user, department, role) triples exist")
}

func TestPublish_EligibilityChecksRunConcurrentlyUpToABound(t *testing.T) {
	var users []uuid.UUID
	var stages []dsl.StageDef
	for i := 0; i < 40; i++ {
		u := uuid.Must(uuid.NewV7())
		users = append(users, u)
		stages = append(stages, stageOf("reviewer", u))
	}
	last := users[len(users)-1]
	plan := &dsl.CompiledPlan{Departments: []dsl.DepartmentDef{{ID: "a", IAMDepartmentID: "dept-a", Stages: stages}}}
	fake := &eligibilityFake{
		delay:    func(uuid.UUID) time.Duration { return 5 * time.Millisecond },
		eligible: func(u uuid.UUID) bool { return u != last },
	}

	start := time.Now()
	err := publishWithAssignees(t, fake, plan)

	require.ErrorIs(t, err, domain.ErrAssigneeIneligible)
	assert.Greater(t, int(fake.peak.Load()), 1, "the checks ran one after another")
	assert.LessOrEqual(t, int(fake.peak.Load()), 8, "the checks are not bounded")
	assert.Less(t, time.Since(start), 150*time.Millisecond, "40 checks of 5ms in sequence take 200ms")
}

func TestPublish_ReportsTheFirstIneligibleAssigneeInPlanOrder(t *testing.T) {
	first, second := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	plan := &dsl.CompiledPlan{Departments: []dsl.DepartmentDef{{
		ID: "a", IAMDepartmentID: "dept-a", Stages: []dsl.StageDef{stageOf("reviewer", first), stageOf("reviewer", second)},
	}}}
	fake := &eligibilityFake{
		delay: func(u uuid.UUID) time.Duration {
			if u == first {
				return 30 * time.Millisecond
			}
			return 0
		},
		eligible: func(uuid.UUID) bool { return false },
	}

	err := publishWithAssignees(t, fake, plan)

	require.ErrorIs(t, err, domain.ErrAssigneeIneligible)
	assert.Contains(t, err.Error(), first.String())
	assert.NotContains(t, err.Error(), second.String())
}

func TestPublish_ALookupErrorIsReportedNotTreatedAsIneligible(t *testing.T) {
	u := uuid.Must(uuid.NewV7())
	plan := &dsl.CompiledPlan{Departments: []dsl.DepartmentDef{{ID: "a", IAMDepartmentID: "dept-a", Stages: []dsl.StageDef{stageOf("reviewer", u)}}}}
	down := errors.New("membership down")
	ctrl := gomock.NewController(t)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	membership := mocks.NewMockMembershipService(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: vRepo, Compiler: compiler, Membership: membership})
	tenantID, wfID, vID := uuid.New(), uuid.New(), uuid.New()
	vRepo.EXPECT().GetByID(gomock.Any(), tenantID, vID).Return(&domain.WorkflowVersion{WorkflowID: wfID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>"}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), "<bpmn/>").Return(nil, nil, nil)
	compiler.EXPECT().BundleModules("<bpmn/>", gomock.Any()).Return("<bpmn/>", nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), "<bpmn/>").Return(collabOf(plan), nil)
	compiler.EXPECT().Hash(gomock.Any(), "<bpmn/>").Return("hash", nil)
	membership.EXPECT().CheckEligibility(gomock.Any(), tenantID, u, "dept-a", "reviewer").Return(false, down)

	_, err := svc.Publish(context.Background(), tenantID, uuid.New(), wfID, vID, false)

	require.ErrorIs(t, err, down)
	assert.NotErrorIs(t, err, domain.ErrAssigneeIneligible)
}
