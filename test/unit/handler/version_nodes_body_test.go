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
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type sgDErrorEntry struct {
	msg    string
	fields map[string]any
}

type sgDErrorLog struct {
	mu      sync.Mutex
	entries []sgDErrorEntry
}

func (l *sgDErrorLog) Error(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, sgDErrorEntry{msg: msg, fields: fields})
}
func (*sgDErrorLog) Debug(string, map[string]any) {}
func (*sgDErrorLog) Info(string, map[string]any)  {}
func (*sgDErrorLog) Warn(string, map[string]any)  {}

func (l *sgDErrorLog) snapshot() []sgDErrorEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]sgDErrorEntry(nil), l.entries...)
}

func sgDGet(router *gin.Engine, path string) *httptest.ResponseRecorder {
	return do(router, req(http.MethodGet, path, nil))
}

func sgDNodesPath() string {
	return "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/nodes"
}

func sgDRealNodesRouter(t *testing.T, v *domain.WorkflowVersion, getErr error, log *sgDErrorLog) (do func() (int, []byte)) {
	t.Helper()
	repo := mocks.NewMockWorkflowVersionRepository(gomock.NewController(t))
	repo.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(v, getErr)
	svc := service.NewVersionService(service.VersionDeps{Versions: repo, Compiler: bpmn_compiler.New()})
	services := handler.Services{Versions: svc}
	if log != nil {
		services.Log = log
	}
	router := newRouter(handler.New(services))
	return func() (int, []byte) {
		w := sgDGet(router, sgDNodesPath())
		return w.Code, w.Body.Bytes()
	}
}

func TestGetVersionNodes_StageWithoutDefaultAssignees_HasAnEmptyListNotNull(t *testing.T) {
	t.Parallel()

	n := int32(2)
	vs := &fakeVersionSvc{nodes: func(_ context.Context, tenantID, workflowID, versionID uuid.UUID) (*service.VersionNodes, error) {
		assert.Equal(t, testTenantID, tenantID)
		assert.Equal(t, testWFID, workflowID)
		assert.Equal(t, testVerID, versionID)
		return &service.VersionNodes{
			Version: &domain.WorkflowVersion{ID: testVerID, WorkflowID: testWFID, VersionNumber: &n},
			Nodes: []service.PlanNode{
				{Key: "Eng/Open", NodeID: "Open", StageType: "prep", Role: "L2", DepartmentID: "Eng", DefaultUserIDs: nil},
				{Key: "Eng/Empty", NodeID: "Empty", StageType: "prep", Role: "L2", DepartmentID: "Eng", DefaultUserIDs: []string{}},
				{Key: "Eng/Named", NodeID: "Named", StageType: "prep", Role: "L2", DepartmentID: "Eng", DefaultUserIDs: []string{"u-1", "u-2"}},
			},
		}, nil
	}}

	w := sgDGet(newRouter(newHandler(nil, nil, vs, nil)), sgDNodesPath())

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body struct {
		Nodes []map[string]json.RawMessage `json:"nodes"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Nodes, 3)
	assert.JSONEq(t, `[]`, string(body.Nodes[0]["default_user_ids"]))
	assert.JSONEq(t, `[]`, string(body.Nodes[1]["default_user_ids"]))
	assert.JSONEq(t, `["u-1","u-2"]`, string(body.Nodes[2]["default_user_ids"]))
}

func sgDStoredPlan(t *testing.T, c dsl.CompiledCollaboration) *domain.WorkflowVersion {
	t.Helper()
	c.SchemaVersion = dsl.CurrentSchemaVersion
	raw, err := json.Marshal(c)
	require.NoError(t, err)
	stored := string(raw)
	n := int32(1)
	return &domain.WorkflowVersion{
		ID: testVerID, WorkflowID: testWFID, TenantID: testTenantID,
		Status: domain.VersionStatusPublished, VersionNumber: &n, CompiledPlanJSON: &stored,
	}
}

func TestGetVersionNodes_StoredPlanThatDoesNotExpand_Returns500WithoutInternalText(t *testing.T) {
	t.Parallel()

	manyStages := make([]dsl.StageDef, domain.MaxPlanStages+1)
	for i := range manyStages {
		manyStages[i] = dsl.StageDef{Type: "prep", NodeID: fmt.Sprintf("T%d", i), Role: "L2"}
	}
	cases := []struct {
		name     string
		plan     dsl.CompiledCollaboration
		wantLeak []string
		wantLog  string
	}{
		{
			name: "a plan that calls itself",
			plan: dsl.CompiledCollaboration{MainPlan: "main", Plans: []*dsl.CompiledPlan{{
				Name:        "main",
				Departments: []dsl.DepartmentDef{{ID: "Eng", Stages: []dsl.StageDef{{Type: "prep", NodeID: "T1", Role: "L2"}}}},
				Execution:   dsl.ExecutionPlan{Steps: []dsl.ExecutionStep{{CallPlan: &dsl.CallPlanStep{NodeID: "Call_Self", Plan: "main"}}}},
			}}},
			wantLeak: []string{"cycle", "Call_Self"},
			wantLog:  "closes a call cycle",
		},
		{
			name: "a call to a plan that is not stored",
			plan: dsl.CompiledCollaboration{MainPlan: "main", Plans: []*dsl.CompiledPlan{{
				Name:        "main",
				Departments: []dsl.DepartmentDef{{ID: "Eng", Stages: []dsl.StageDef{{Type: "prep", NodeID: "T1", Role: "L2"}}}},
				Execution:   dsl.ExecutionPlan{Steps: []dsl.ExecutionStep{{CallPlan: &dsl.CallPlanStep{NodeID: "Call_Ghost", Plan: "ghost-plan"}}}},
			}}},
			wantLeak: []string{"ghost-plan", "Call_Ghost"},
			wantLog:  "ghost-plan",
		},
		{
			name: "more stages than a plan may hold",
			plan: dsl.CompiledCollaboration{MainPlan: "main", Plans: []*dsl.CompiledPlan{{
				Name:        "main",
				Departments: []dsl.DepartmentDef{{ID: "Eng", Stages: manyStages}},
				Execution:   dsl.ExecutionPlan{Steps: []dsl.ExecutionStep{{Sequential: []string{"Eng"}}}},
			}}},
			wantLeak: []string{"1000", "more than"},
			wantLog:  "more than 1000 stages",
		},
		{
			name: "a main plan that names no stored plan",
			plan: dsl.CompiledCollaboration{MainPlan: "missing-main", Plans: []*dsl.CompiledPlan{{
				Name:        "other",
				Departments: []dsl.DepartmentDef{{ID: "Eng", Stages: []dsl.StageDef{{Type: "prep", NodeID: "T1", Role: "L2"}}}},
				Execution:   dsl.ExecutionPlan{Steps: []dsl.ExecutionStep{{Sequential: []string{"Eng"}}}},
			}}},
			wantLeak: []string{"missing-main", "not among"},
			wantLog:  "not among the compiled plans",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			log := &sgDErrorLog{}

			code, body := sgDRealNodesRouter(t, sgDStoredPlan(t, tc.plan), nil, log)()

			require.Equal(t, http.StatusInternalServerError, code, string(body))
			var prob map[string]any
			require.NoError(t, json.Unmarshal(body, &prob))
			assert.Equal(t, "INTERNAL_ERROR", prob["code"])
			assert.Equal(t, "an unexpected error occurred", prob["detail"])
			assert.True(t, strings.HasSuffix(prob["type"].(string), "/internal-error"), "type = %v", prob["type"])
			for _, leak := range tc.wantLeak {
				assert.NotContains(t, string(body), leak)
			}
			entries := log.snapshot()
			require.Len(t, entries, 1)
			assert.Equal(t, "request failed", entries[0].msg)
			assert.Equal(t, testTenantID.String(), entries[0].fields["tenant_id"])
			assert.Equal(t, testUserID.String(), entries[0].fields["user_id"])
			assert.Contains(t, entries[0].fields["error"], testVerID.String())
			assert.Contains(t, entries[0].fields["error"], tc.wantLog)
		})
	}
}

func TestGetVersionNodes_UnclassifiedLookupFailure_Returns500WithGenericDetailAndLogsIt(t *testing.T) {
	t.Parallel()

	log := &sgDErrorLog{}
	lookupErr := errors.New("pq: relation workflow_version is corrupted at 10.0.0.7")

	code, body := sgDRealNodesRouter(t, nil, lookupErr, log)()

	require.Equal(t, http.StatusInternalServerError, code, string(body))
	var prob map[string]any
	require.NoError(t, json.Unmarshal(body, &prob))
	assert.Equal(t, "INTERNAL_ERROR", prob["code"])
	assert.Equal(t, "an unexpected error occurred", prob["detail"])
	assert.Equal(t, float64(http.StatusInternalServerError), prob["status"])
	assert.Equal(t, sgDNodesPath(), prob["instance"])
	for _, leak := range []string{"pq:", "corrupted", "10.0.0.7", "workflow_version"} {
		assert.NotContains(t, string(body), leak)
	}
	entries := log.snapshot()
	require.Len(t, entries, 1)
	assert.Equal(t, "request failed", entries[0].msg)
	assert.Equal(t, 500, entries[0].fields["status"])
	assert.Contains(t, entries[0].fields["error"], "corrupted")
	assert.Equal(t, testTenantID.String(), entries[0].fields["tenant_id"])
}

func TestGetVersionNodes_NoSuchVersion_ReturnsNotFoundProblem(t *testing.T) {
	t.Parallel()

	code, body := sgDRealNodesRouter(t, nil, fmt.Errorf("lookup: %w", domain.ErrNotFound), nil)()

	require.Equal(t, http.StatusNotFound, code, string(body))
	var prob map[string]any
	require.NoError(t, json.Unmarshal(body, &prob))
	assert.Equal(t, "NOT_FOUND", prob["code"])
	assert.Equal(t, "https://api.workflow.platform/errors/not-found", prob["type"])
	assert.Equal(t, sgDNodesPath(), prob["instance"])
}
