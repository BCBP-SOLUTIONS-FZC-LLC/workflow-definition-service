package handler_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
)

func envelopeEdgePost(services handler.Services, rawBody []byte) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	httpadapter.RegisterInternalRoutes(r.Group("/api/v1/internal"), handler.New(services))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/events", bytes.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func envelopeEdgeMarshal(t *testing.T, body any) []byte {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	return raw
}

func TestInternalEvents_UnreadableEnvelope_400(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "an empty body", body: ""},
		{
			name: "an event id that is a number",
			body: `{"id":42,"type":"DepartmentMembershipRevoked","tenant_id":"` + ieTenantID.String() + `","data":{}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rev := &fakeMembershipRevoker{}
			off := &fakeOffboarder{}

			w := envelopeEdgePost(handler.Services{Membership: rev, Offboarding: off}, []byte(tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, "invalid event envelope", prob["detail"])
			assert.False(t, rev.called || rev.tenantCalled, "an unreadable envelope must not reach the service")
			assert.Empty(t, off.recorded)
			assert.Empty(t, off.returned)
		})
	}
}

func TestInternalEvents_ReturnedTenantRecordingFailure_500(t *testing.T) {
	cases := []struct {
		name   string
		status string
	}{
		{name: "an active tenant", status: "active"},
		{name: "a status with the wrong case", status: "Offboarded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			off := &fakeOffboarder{err: errors.New("db down")}
			body := envelope("TenantStateChanged", ieEventID.String(), ieTenantID.String(), map[string]string{"status": tc.status})
			raw := envelopeEdgeMarshal(t, body)

			w := envelopeEdgePost(handler.Services{Membership: &fakeMembershipRevoker{}, Offboarding: off}, raw)

			require.Equal(t, http.StatusInternalServerError, w.Code, "a failed write must be redelivered, not acknowledged")
			prob := decodeProblem(t, w)
			assert.Equal(t, "INTERNAL_ERROR", prob["code"])
			assert.Equal(t, "failed to process event", prob["detail"])
			assert.NotContains(t, w.Body.String(), "db down")
			assert.Equal(t, []uuid.UUID{ieTenantID}, off.returned)
			assert.Empty(t, off.recorded)
		})
	}
}

func TestInternalEvents_ServiceErrorWithoutALogger_500(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		payload   map[string]string
	}{
		{
			name:      "a department revocation",
			eventType: "DepartmentMembershipRevoked",
			payload:   map[string]string{"user_id": ieUserID.String(), "department_id": ieDeptID},
		},
		{
			name:      "a tenant revocation",
			eventType: "MembershipRevoked",
			payload:   map[string]string{"user_id": ieUserID.String()},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rev := &fakeMembershipRevoker{err: errors.New("db failure")}
			raw := envelopeEdgeMarshal(t, envelope(tc.eventType, ieEventID.String(), ieTenantID.String(), tc.payload))

			w := envelopeEdgePost(handler.Services{Membership: rev}, raw)

			require.Equal(t, http.StatusInternalServerError, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "INTERNAL_ERROR", prob["code"])
			assert.Equal(t, "failed to process event", prob["detail"])
			assert.NotContains(t, w.Body.String(), "db failure")
			assert.True(t, rev.called || rev.tenantCalled, "the failure must come from the service, not from validation")
		})
	}
}
