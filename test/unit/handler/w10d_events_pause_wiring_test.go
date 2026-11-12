package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	outboundhttp "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

const (
	w10dExecToken        = "w10d-execution-token"
	w10dEventsPath       = "/api/v1/internal/events"
	w10dProblemBase      = "https://api.workflow.platform/errors/"
	w10dInvalidationText = "Default assignee is no longer eligible: Department membership revoked"
)

var (
	w10dTenantID = uuid.MustParse("aaaaaaaa-0000-4000-8000-00000000d101")
	w10dUserID   = uuid.MustParse("bbbbbbbb-0000-4000-8000-00000000d102")
	w10dDeptID   = uuid.MustParse("cccccccc-0000-4000-8000-00000000d103")
	w10dVersion  = uuid.MustParse("dddddddd-0000-4000-8000-00000000d104")
	w10dEventID  = uuid.MustParse("eeeeeeee-0000-4000-8000-00000000d105")
)

type w10dExecCall struct {
	method, path, token, contentType, body string
}

type w10dExecServer struct {
	*httptest.Server
	mu    sync.Mutex
	calls []w10dExecCall
}

func w10dNewExecServer(t *testing.T, statuses ...int) *w10dExecServer {
	t.Helper()
	s := &w10dExecServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.calls = append(s.calls, w10dExecCall{
			method: r.Method, path: r.URL.Path, token: r.Header.Get("x-internal-token"),
			contentType: r.Header.Get("Content-Type"), body: string(b),
		})
		n := len(s.calls)
		s.mu.Unlock()
		status := statuses[len(statuses)-1]
		if n <= len(statuses) {
			status = statuses[n-1]
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *w10dExecServer) seen() []w10dExecCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]w10dExecCall(nil), s.calls...)
}

func w10dRegisterMetrics(t *testing.T) {
	t.Helper()
	log, err := logger.NewLogger("dev")
	require.NoError(t, err)
	_ = gincommon.ObservabilityMiddlewares(gincommon.Config{
		Logger: log, ServiceName: "workflow-definition-service", BuildVersion: "test",
		Domain: observability.Domain, Environment: "test",
	})
	observability.Register()
}

func w10dIngest(eventType, outcome string) float64 {
	return testutil.ToFloat64(observability.InternalEventsIngestTotal.WithLabelValues(eventType, outcome))
}

type w10dEventsRig struct {
	assignees *mocks.MockAssigneeRepository
	versions  *mocks.MockWorkflowVersionRepository
	router    *gin.Engine
}

func w10dNewEventsRig(t *testing.T, executionURL string) *w10dEventsRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctrl := gomock.NewController(t)
	rig := &w10dEventsRig{
		assignees: mocks.NewMockAssigneeRepository(ctrl),
		versions:  mocks.NewMockWorkflowVersionRepository(ctrl),
	}
	svc := service.NewVersionService(service.VersionDeps{
		Versions: rig.versions, Assignees: rig.assignees, Log: testkit.FakeLogger{},
		Execution: outboundhttp.NewExecutionClient(executionURL, w10dExecToken, 5*time.Second),
	})
	r := gin.New()
	httpadapter.RegisterInternalRoutes(r.Group("/api/v1/internal"), handler.New(handler.Services{Membership: svc, Log: testkit.FakeLogger{}}))
	rig.router = r
	return rig
}

func w10dDepartmentEvent() map[string]any {
	data, _ := json.Marshal(map[string]string{"user_id": w10dUserID.String(), "department_id": w10dDeptID.String()})
	return map[string]any{
		"id": w10dEventID.String(), "type": "DepartmentMembershipRevoked", "tenant_id": w10dTenantID.String(),
		"time": time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339), "data": json.RawMessage(data),
	}
}

func w10dTenantEvent() map[string]any {
	data, _ := json.Marshal(map[string]string{"user_id": w10dUserID.String()})
	return map[string]any{
		"id": w10dEventID.String(), "type": "MembershipRevoked", "tenant_id": w10dTenantID.String(),
		"time": time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339), "data": json.RawMessage(data),
	}
}

func (r *w10dEventsRig) post(ctx context.Context, event map[string]any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(event)
	httpReq := httptest.NewRequest(http.MethodPost, w10dEventsPath, bytes.NewReader(raw)).WithContext(ctx)
	httpReq.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.router.ServeHTTP(w, httpReq)
	return w
}

func (r *w10dEventsRig) expectNoAssigneeRows() {
	r.assignees.EXPECT().ListByUser(gomock.Any(), w10dTenantID, w10dUserID).Return(nil, nil)
}

func w10dPausePath() string {
	return "/api/v1/internal/users/" + w10dUserID.String() + "/pause-tasks"
}

func w10dDepartmentPauseBody() string {
	return `{"department_id":"` + w10dDeptID.String() + `","tenant_id":"` + w10dTenantID.String() + `"}`
}

func w10dAssertEventFailureBody(t *testing.T, w *httptest.ResponseRecorder, secrets ...string) {
	t.Helper()
	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, w10dProblemBase+"internal-error", body["type"])
	assert.Equal(t, "INTERNAL_ERROR", body["code"])
	assert.Equal(t, "failed to process event", body["detail"])
	assert.EqualValues(t, http.StatusInternalServerError, body["status"])
	assert.Equal(t, w10dEventsPath, body["instance"])
	for _, secret := range secrets {
		assert.NotContains(t, w.Body.String(), secret, "the execution service's address and the call's text stay out of the answer")
	}
}

func TestInternalEvents_PauseTasksFailingWithAServerErrorOnEveryAttempt_Is500AfterThreeCallsAndKeepsTheInvalidation(t *testing.T) {
	w10dRegisterMetrics(t)
	exec := w10dNewExecServer(t, http.StatusInternalServerError)
	rig := w10dNewEventsRig(t, exec.URL)
	rig.assignees.EXPECT().ListByUser(gomock.Any(), w10dTenantID, w10dUserID).Return([]*domain.NodeAssignee{
		{ID: uuid.New(), TenantID: w10dTenantID, WorkflowVersionID: w10dVersion, NodeKey: "w10d-task", UserID: w10dUserID, DepartmentID: w10dDeptID},
	}, nil)
	rig.versions.EXPECT().GetByID(gomock.Any(), w10dTenantID, w10dVersion).
		Return(&domain.WorkflowVersion{ID: w10dVersion, WorkflowID: uuid.New(), Status: domain.VersionStatusDraft}, nil).Times(1)
	rig.versions.EXPECT().SetInvalid(gomock.Any(), w10dTenantID, w10dVersion, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ uuid.UUID, errorsJSON string) error {
			assert.JSONEq(t, `[{"node_id":"w10d-task","error":"`+w10dInvalidationText+`"}]`, errorsJSON)
			return nil
		}).Times(1)
	before := w10dIngest("DepartmentMembershipRevoked", "error")
	okBefore := w10dIngest("DepartmentMembershipRevoked", "ok")

	w := rig.post(context.Background(), w10dDepartmentEvent())

	w10dAssertEventFailureBody(t, w, exec.URL, "pause")
	calls := exec.seen()
	require.Len(t, calls, 3)
	for _, c := range calls {
		assert.Equal(t, w10dExecCall{
			method: http.MethodPost, path: w10dPausePath(), token: w10dExecToken,
			contentType: "application/json", body: w10dDepartmentPauseBody(),
		}, c)
	}
	assert.Equal(t, before+1, w10dIngest("DepartmentMembershipRevoked", "error"))
	assert.Equal(t, okBefore, w10dIngest("DepartmentMembershipRevoked", "ok"))
}

func TestInternalEvents_PauseTasksFailingOnceThenAccepted_Is200WithAnEmptyBodyAndCountsOk(t *testing.T) {
	w10dRegisterMetrics(t)
	cases := []struct {
		name      string
		eventType string
		event     func() map[string]any
		wantBody  string
	}{
		{"a department revocation", "DepartmentMembershipRevoked", w10dDepartmentEvent, w10dDepartmentPauseBody()},
		{"a full membership revocation", "MembershipRevoked", w10dTenantEvent, `{"tenant_id":"` + w10dTenantID.String() + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exec := w10dNewExecServer(t, http.StatusServiceUnavailable, http.StatusNoContent)
			rig := w10dNewEventsRig(t, exec.URL)
			rig.expectNoAssigneeRows()
			okBefore := w10dIngest(tc.eventType, "ok")
			errBefore := w10dIngest(tc.eventType, "error")

			w := rig.post(context.Background(), tc.event())

			assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Empty(t, w.Body.String())
			calls := exec.seen()
			require.Len(t, calls, 2)
			for _, c := range calls {
				assert.Equal(t, w10dExecCall{
					method: http.MethodPost, path: w10dPausePath(), token: w10dExecToken,
					contentType: "application/json", body: tc.wantBody,
				}, c)
			}
			assert.Equal(t, okBefore+1, w10dIngest(tc.eventType, "ok"))
			assert.Equal(t, errBefore, w10dIngest(tc.eventType, "error"))
		})
	}
}

func TestInternalEvents_PauseTasksAnsweredWithAnUnacceptedStatus_Is500AfterOneCall(t *testing.T) {
	w10dRegisterMetrics(t)
	for _, status := range []int{http.StatusNotFound, http.StatusBadRequest, http.StatusOK, http.StatusAccepted} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			exec := w10dNewExecServer(t, status)
			rig := w10dNewEventsRig(t, exec.URL)
			rig.expectNoAssigneeRows()
			before := w10dIngest("DepartmentMembershipRevoked", "error")

			w := rig.post(context.Background(), w10dDepartmentEvent())

			w10dAssertEventFailureBody(t, w, exec.URL, strconv.Itoa(status))
			assert.Len(t, exec.seen(), 1, "only 204 is accepted and nothing but a 5xx is retried")
			assert.Equal(t, before+1, w10dIngest("DepartmentMembershipRevoked", "error"))
		})
	}

	t.Run("an address that cannot become a request", func(t *testing.T) {
		rig := w10dNewEventsRig(t, "http://[w10d-bad-host")
		rig.expectNoAssigneeRows()

		w := rig.post(context.Background(), w10dDepartmentEvent())

		w10dAssertEventFailureBody(t, w, "w10d-bad-host")
	})
}

func TestInternalEvents_PauseTasksWithTheExecutionServiceUnreachableOrTheCallerGone_Is500(t *testing.T) {
	w10dRegisterMetrics(t)

	t.Run("connection refused", func(t *testing.T) {
		rig := w10dNewEventsRig(t, "http://127.0.0.1:1")
		rig.expectNoAssigneeRows()
		before := w10dIngest("DepartmentMembershipRevoked", "error")

		w := rig.post(context.Background(), w10dDepartmentEvent())

		w10dAssertEventFailureBody(t, w, "127.0.0.1")
		assert.Equal(t, before+1, w10dIngest("DepartmentMembershipRevoked", "error"))
	})

	t.Run("request context already cancelled", func(t *testing.T) {
		exec := w10dNewExecServer(t, http.StatusNoContent)
		rig := w10dNewEventsRig(t, exec.URL)
		rig.expectNoAssigneeRows()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		w := rig.post(ctx, w10dDepartmentEvent())

		w10dAssertEventFailureBody(t, w, exec.URL)
		assert.Empty(t, exec.seen(), "nothing is sent for a caller that is already gone")
	})
}
