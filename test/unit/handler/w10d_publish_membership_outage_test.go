package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	outboundhttp "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

const w10dPublishXML = "<w10d-draft/>"

var w10dAssignee = uuid.MustParse("01900000-0000-7000-8000-00000000d201")

type w10dMembershipCall struct {
	method, path, department, level, userID, roles, tenant string
}

type w10dMembershipServer struct {
	*httptest.Server
	mu    sync.Mutex
	calls []w10dMembershipCall
}

func w10dNewMembershipServer(t *testing.T, answer func(n int, w http.ResponseWriter, r *http.Request)) *w10dMembershipServer {
	t.Helper()
	s := &w10dMembershipServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls = append(s.calls, w10dMembershipCall{
			method: r.Method, path: r.URL.Path,
			department: r.URL.Query().Get("department_id"), level: r.URL.Query().Get("required_level"),
			userID: r.Header.Get("x-user-id"), roles: r.Header.Get("x-tenant-roles"), tenant: r.Header.Get("x-tenant-id"),
		})
		n := len(s.calls)
		s.mu.Unlock()
		answer(n, w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *w10dMembershipServer) seen() []w10dMembershipCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]w10dMembershipCall(nil), s.calls...)
}

func w10dAlwaysStatus(status int) func(int, http.ResponseWriter, *http.Request) {
	return func(_ int, w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }
}

type w10dPublishRig struct {
	router *gin.Engine
	iamDep string
}

func w10dNewPublishRig(t *testing.T, membershipURL string, timeout time.Duration) *w10dPublishRig {
	t.Helper()
	ctrl := gomock.NewController(t)
	versions := mocks.NewMockWorkflowVersionRepository(ctrl)
	workflows := mocks.NewMockWorkflowRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	rig := &w10dPublishRig{iamDep: uuid.NewString()}
	plan := &dsl.CompiledPlan{Name: "w10d-main", Departments: []dsl.DepartmentDef{{
		ID: "w10d-dept", Label: "w10d-dept", IAMDepartmentID: rig.iamDep,
		Stages: []dsl.StageDef{{Type: "review", NodeID: "w10d-task", Role: "reviewer", DefaultAssignees: []string{w10dAssignee.String()}}},
	}}}
	versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(&domain.WorkflowVersion{
		ID: testVerID, WorkflowID: testWFID, TenantID: testTenantID, Status: domain.VersionStatusDraft, BPMNXML: w10dPublishXML, RecordVersion: 1,
	}, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), w10dPublishXML).Return(nil, nil, nil)
	compiler.EXPECT().BundleModules(w10dPublishXML, gomock.Len(0)).Return(w10dPublishXML, nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), w10dPublishXML).Return(
		&dsl.CompiledCollaboration{MainPlan: plan.Name, Plans: []*dsl.CompiledPlan{plan}, SchemaVersion: dsl.CurrentSchemaVersion}, nil)
	compiler.EXPECT().Hash(gomock.Any(), w10dPublishXML).Return("w10d-hash", nil)
	workflows.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(&domain.Workflow{ID: testWFID}, nil)

	svc := service.NewVersionService(service.VersionDeps{
		Workflows: workflows, Versions: versions, Compiler: compiler, Log: testkit.FakeLogger{},
		Membership: outboundhttp.NewMembershipClient(membershipURL, timeout),
	})
	rig.router = newRouter(handler.New(handler.Services{Versions: svc, Log: testkit.FakeLogger{}}))
	return rig
}

func w10dPublishPath() string {
	return "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
}

func (r *w10dPublishRig) publish(ctx context.Context) *httptest.ResponseRecorder {
	return do(r.router, adminReq(http.MethodPost, w10dPublishPath(), nil).WithContext(ctx))
}

func w10dAssertPublish503(t *testing.T, w *httptest.ResponseRecorder, hidden ...string) {
	t.Helper()
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, w10dProblemBase+"service-unavailable", body["type"])
	assert.Equal(t, "UPSTREAM_UNAVAILABLE", body["code"])
	assert.Equal(t, "an upstream dependency is temporarily unavailable", body["detail"])
	assert.EqualValues(t, http.StatusServiceUnavailable, body["status"])
	assert.Equal(t, w10dPublishPath(), body["instance"])
	for _, h := range hidden {
		assert.NotContains(t, w.Body.String(), h)
	}
}

func (r *w10dPublishRig) wantEligibilityRequest(t *testing.T, c w10dMembershipCall) {
	t.Helper()
	assert.Equal(t, w10dMembershipCall{
		method:     http.MethodGet,
		path:       "/api/v1/internal/tenants/" + testTenantID.String() + "/members/" + w10dAssignee.String() + "/eligibility",
		department: r.iamDep, level: "reviewer",
		userID: "iam-system", roles: "iam-system", tenant: testTenantID.String(),
	}, c)
}

func TestPublishVersion_MembershipEligibilityFailingOnEveryAttempt_Is503UpstreamUnavailable(t *testing.T) {
	blockUntilTheClientGivesUp := func(_ int, _ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	cases := []struct {
		name      string
		answer    func(int, http.ResponseWriter, *http.Request)
		timeout   time.Duration
		refused   bool
		wantCalls int
	}{
		{name: "502 on every attempt", answer: w10dAlwaysStatus(http.StatusBadGateway), timeout: 5 * time.Second, wantCalls: 3},
		{name: "500 on every attempt", answer: w10dAlwaysStatus(http.StatusInternalServerError), timeout: 5 * time.Second, wantCalls: 3},
		{name: "no answer before the client timeout on every attempt", answer: blockUntilTheClientGivesUp, timeout: 250 * time.Millisecond, wantCalls: 3},
		{name: "an answer that is not a 200 or a 5xx", answer: w10dAlwaysStatus(http.StatusConflict), timeout: 5 * time.Second, wantCalls: 1},
		{name: "a 200 that is not json", answer: func(_ int, w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{bad")) }, timeout: 5 * time.Second, wantCalls: 1},
		{name: "a 200 without the eligible flag", answer: func(_ int, w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"reason":"w10d"}`)) }, timeout: 5 * time.Second, wantCalls: 1},
		{name: "connection refused", refused: true, timeout: 5 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := "http://127.0.0.1:1"
			var srv *w10dMembershipServer
			if !tc.refused {
				srv = w10dNewMembershipServer(t, tc.answer)
				url = srv.URL
			}
			rig := w10dNewPublishRig(t, url, tc.timeout)

			w := rig.publish(context.Background())

			w10dAssertPublish503(t, w, url)
			if srv == nil {
				return
			}
			calls := srv.seen()
			require.Len(t, calls, tc.wantCalls)
			for _, c := range calls {
				rig.wantEligibilityRequest(t, c)
			}
		})
	}
}

func TestPublishVersion_CallerGoneWhileMembershipIsRetried_Is503UpstreamUnavailable(t *testing.T) {
	t.Run("cancelled while the first attempt is answered with a 503", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		srv := w10dNewMembershipServer(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
			cancel()
			w.WriteHeader(http.StatusServiceUnavailable)
		})
		rig := w10dNewPublishRig(t, srv.URL, time.Minute)

		w := rig.publish(ctx)

		w10dAssertPublish503(t, w, srv.URL)
		calls := srv.seen()
		require.Len(t, calls, 1, "the wait between attempts ends as soon as the caller is gone")
		rig.wantEligibilityRequest(t, calls[0])
	})

	t.Run("the request deadline passes before the next attempt", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		srv := w10dNewMembershipServer(t, w10dAlwaysStatus(http.StatusServiceUnavailable))
		rig := w10dNewPublishRig(t, srv.URL, time.Minute)

		w := rig.publish(ctx)

		w10dAssertPublish503(t, w, srv.URL)
		assert.LessOrEqual(t, len(srv.seen()), 1, "the first backoff is 50 ms, longer than the deadline, so no second attempt starts")
	})
}
