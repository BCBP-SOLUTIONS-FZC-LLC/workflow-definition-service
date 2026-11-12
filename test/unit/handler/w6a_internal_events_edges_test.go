package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

func w6aEventBody(t *testing.T, eventType, tenantID, dataJSON string) string {
	t.Helper()
	typeJSON, err := json.Marshal(eventType)
	require.NoError(t, err)
	tenantJSON, err := json.Marshal(tenantID)
	require.NoError(t, err)
	body := `{"id":"` + ieEventID.String() + `","type":` + string(typeJSON) +
		`,"tenant_id":` + string(tenantJSON) + `,"time":"` + ieEventTime.Format("2006-01-02T15:04:05Z07:00") + `"`
	if dataJSON != "" {
		body += `,"data":` + dataJSON
	}
	return body + `}`
}

func w6aPostEvent(rev *fakeMembershipRevoker, off *fakeOffboarder, raw string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	h := handler.New(handler.Services{Membership: rev, Offboarding: off, Log: testkit.FakeLogger{}})
	r := gin.New()
	httpadapter.RegisterInternalRoutes(r.Group("/api/v1/internal"), h)
	w := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/internal/events", bytes.NewReader([]byte(raw)))
	httpReq.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, httpReq)
	return w
}

func w6aValidPayload() string {
	return `{"user_id":"` + ieUserID.String() + `","department_id":"` + ieDeptID + `","status":"offboarded"}`
}

func TestInternalEvents_TypeSpellingDifferingFromAHandledTypeIsAcknowledgedWithoutAnyEffect(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		eventType string
	}{
		{name: "empty type", eventType: ""},
		{name: "tenant revocation in lower case", eventType: "membershiprevoked"},
		{name: "tenant revocation in upper case", eventType: "MEMBERSHIPREVOKED"},
		{name: "department revocation in lower case", eventType: "departmentmembershiprevoked"},
		{name: "department revocation with a leading lower case", eventType: "departmentMembershipRevoked"},
		{name: "purge in lower case", eventType: "tenantmembershipspurged"},
		{name: "state change in lower case", eventType: "tenantstatechanged"},
		{name: "known type with a trailing space", eventType: "MembershipRevoked "},
		{name: "unrelated mixed case name", eventType: "SomethingNew"},
		{name: "single character name", eventType: "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rev := &fakeMembershipRevoker{}
			off := &fakeOffboarder{}

			w := w6aPostEvent(rev, off, w6aEventBody(t, tc.eventType, ieTenantID.String(), w6aValidPayload()))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Empty(t, w.Body.String())
			assert.False(t, rev.called || rev.tenantCalled, "a type that is not handled reaches no revocation")
			assert.Empty(t, off.recorded)
			assert.Empty(t, off.returned)
		})
	}
}

func TestInternalEvents_MembershipRevoked_DepartmentIDIsNotRequiredOrChecked(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		payload string
	}{
		{name: "absent", payload: `{"user_id":"` + ieUserID.String() + `"}`},
		{name: "a name that is not a UUID", payload: `{"user_id":"` + ieUserID.String() + `","department_id":"engineering"}`},
		{name: "empty", payload: `{"user_id":"` + ieUserID.String() + `","department_id":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rev := &fakeMembershipRevoker{}
			off := &fakeOffboarder{}

			w := w6aPostEvent(rev, off, w6aEventBody(t, "MembershipRevoked", ieTenantID.String(), tc.payload))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Empty(t, w.Body.String())
			assert.True(t, rev.tenantCalled, "the tenant-wide revocation runs")
			assert.False(t, rev.called, "the department-scoped revocation does not")
			assert.Equal(t, ieEventID, rev.gotEventID)
			assert.Equal(t, ieTenantID, rev.gotTenantID)
			assert.Equal(t, ieUserID, rev.gotUserID)
			assert.True(t, ieEventTime.Equal(rev.gotAt), "the envelope time reaches the service, got %s", rev.gotAt)
		})
	}
}

func TestInternalEvents_TenantMembershipsPurged_PayloadIsNeverRead(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		data string
	}{
		{name: "no data member", data: ""},
		{name: "null", data: `null`},
		{name: "a string", data: `"just a string"`},
		{name: "a number", data: `123`},
		{name: "an array", data: `[1,2,3]`},
		{name: "an object with unrelated members", data: `{"unexpected":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			off := &fakeOffboarder{}

			w := w6aPostEvent(&fakeMembershipRevoker{}, off, w6aEventBody(t, "TenantMembershipsPurged", ieTenantID.String(), tc.data))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Empty(t, w.Body.String())
			assert.Equal(t, []uuid.UUID{ieTenantID}, off.recorded)
			require.Len(t, off.gotAt, 1)
			assert.True(t, ieEventTime.Equal(off.gotAt[0]), "the envelope time anchors the retention period, got %s", off.gotAt[0])
			assert.Empty(t, off.returned)
		})
	}
}

func TestInternalEvents_TenantStateChanged_OnlyTheExactLowerCaseStatusOffboardsTheTenant(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		data string
	}{
		{name: "capitalised", data: `{"status":"Offboarded"}`},
		{name: "upper case", data: `{"status":"OFFBOARDED"}`},
		{name: "with a leading space", data: `{"status":" offboarded"}`},
		{name: "empty", data: `{"status":""}`},
		{name: "status missing", data: `{"previous_status":"cancelled"}`},
		{name: "empty object", data: `{}`},
		{name: "null", data: `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			off := &fakeOffboarder{}

			w := w6aPostEvent(&fakeMembershipRevoker{}, off, w6aEventBody(t, "TenantStateChanged", ieTenantID.String(), tc.data))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Empty(t, w.Body.String())
			assert.Equal(t, []uuid.UUID{ieTenantID}, off.returned, "the tenant is marked returned")
			assert.Empty(t, off.recorded, "the tenant is not recorded as offboarded")
		})
	}
}

func TestInternalEvents_RevocationSuccess_IsAnEmptyOKAndCountsOneOkEvent(t *testing.T) {
	log, err := logger.NewLogger("dev")
	require.NoError(t, err)
	_ = gincommon.ObservabilityMiddlewares(gincommon.Config{
		Logger: log, ServiceName: "workflow-definition-service", BuildVersion: "test",
		Domain: observability.Domain, Environment: "test",
	})
	observability.Register()

	cases := []struct {
		name      string
		eventType string
		payload   string
	}{
		{name: "department revocation", eventType: "DepartmentMembershipRevoked", payload: w6aValidPayload()},
		{name: "tenant revocation", eventType: "MembershipRevoked", payload: `{"user_id":"` + ieUserID.String() + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			counter := func(result string) float64 {
				return testutil.ToFloat64(observability.InternalEventsIngestTotal.WithLabelValues(tc.eventType, result))
			}
			okBefore, errorBefore, badBefore := counter("ok"), counter("error"), counter("bad_payload")
			rev := &fakeMembershipRevoker{}

			w := w6aPostEvent(rev, &fakeOffboarder{}, w6aEventBody(t, tc.eventType, ieTenantID.String(), tc.payload))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Zero(t, w.Body.Len(), "a revocation is acknowledged with no body")
			assert.True(t, rev.called || rev.tenantCalled, "the revocation reached the service")
			assert.Equal(t, okBefore+1, counter("ok"))
			assert.Equal(t, errorBefore, counter("error"))
			assert.Equal(t, badBefore, counter("bad_payload"))
		})
	}
}

func TestInternalEvents_TenantEventWithATenantIDThatIsNotAUUID_400BadRequest(t *testing.T) {
	t.Parallel()

	events := []struct {
		name      string
		eventType string
		data      string
	}{
		{name: "state change to offboarded", eventType: "TenantStateChanged", data: `{"status":"offboarded"}`},
		{name: "state change to active", eventType: "TenantStateChanged", data: `{"status":"active"}`},
		{name: "purge", eventType: "TenantMembershipsPurged", data: `{}`},
	}
	tenantIDs := []string{"not-a-uuid", "1234", ieTenantID.String() + "0"}
	for _, ev := range events {
		for _, tenantID := range tenantIDs {
			t.Run(ev.name+" with "+tenantID, func(t *testing.T) {
				t.Parallel()
				off := &fakeOffboarder{}

				w := w6aPostEvent(&fakeMembershipRevoker{}, off, w6aEventBody(t, ev.eventType, tenantID, ev.data))

				require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
				prob := decodeProblem(t, w)
				assert.Equal(t, "https://api.workflow.platform/errors/bad-request", prob["type"])
				assert.Equal(t, "BAD_REQUEST", prob["code"])
				assert.Equal(t, "invalid tenant_id", prob["detail"])
				assert.Empty(t, off.recorded)
				assert.Empty(t, off.returned)
			})
		}
	}
}
