package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

type fakeMembershipRevoker struct {
	called       bool
	tenantCalled bool
	err          error

	gotEventID  uuid.UUID
	gotTenantID uuid.UUID
	gotUserID   uuid.UUID
	gotDeptID   string
}

func (f *fakeMembershipRevoker) HandleTenantMembershipRevoked(
	_ context.Context,
	eventID, tenantID, userID uuid.UUID,
) error {
	f.tenantCalled = true
	f.gotEventID = eventID
	f.gotTenantID = tenantID
	f.gotUserID = userID
	return f.err
}

func (f *fakeMembershipRevoker) HandleMembershipRevoked(
	_ context.Context,
	eventID, tenantID, userID uuid.UUID,
	departmentID string,
) error {
	f.called = true
	f.gotEventID = eventID
	f.gotTenantID = tenantID
	f.gotUserID = userID
	f.gotDeptID = departmentID
	return f.err
}

var (
	ieEventID  = uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	ieTenantID = uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	ieUserID   = uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	ieDeptID   = "dept-finance"
)

func internalRouter(rev *fakeMembershipRevoker) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := handler.New(handler.Services{Membership: rev, Log: testkit.FakeLogger{}})
	r := gin.New()
	r.POST("/internal/events", h.HandleInternalEvent)
	return r
}

func postEvent(rev *fakeMembershipRevoker, body any) *httptest.ResponseRecorder {
	var r = internalRouter(rev)
	raw, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/internal/events", bytes.NewReader(raw))
	httpReq.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, httpReq)
	return w
}

func envelope(eventType, eventID, tenantID string, payload any) map[string]any {
	raw, _ := json.Marshal(payload)
	return map[string]any{
		"id":        eventID,
		"type":      eventType,
		"tenant_id": tenantID,
		"data":      json.RawMessage(raw),
	}
}

func TestInternalEvents_HappyPath(t *testing.T) {
	rev := &fakeMembershipRevoker{}
	w := postEvent(rev, envelope("DepartmentMembershipRevoked", ieEventID.String(), ieTenantID.String(),
		map[string]string{"user_id": ieUserID.String(), "department_id": ieDeptID, "role": "reviewer"}))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, rev.called)
	assert.Equal(t, ieEventID, rev.gotEventID)
	assert.Equal(t, ieTenantID, rev.gotTenantID)
	assert.Equal(t, ieUserID, rev.gotUserID)
	assert.Equal(t, ieDeptID, rev.gotDeptID)
}

// TestInternalEvents_TenantMembershipRevoked covers IAM's tenant-level
// MembershipRevoked, which is a different event from the department-level
// DepartmentMembershipRevoked above and carries no department_id. Before it
// was handled it fell through to the unknown-type branch and was answered
// 200 with no side effects at all, so a user removed from a tenant kept
// their tasks and stayed a live default assignee.
func TestInternalEvents_TenantMembershipRevoked(t *testing.T) {
	rev := &fakeMembershipRevoker{}
	w := postEvent(rev, envelope("MembershipRevoked", ieEventID.String(), ieTenantID.String(),
		map[string]string{"user_id": ieUserID.String()}))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, rev.tenantCalled, "MembershipRevoked must reach the tenant-wide handler")
	assert.False(t, rev.called, "it must NOT be routed to the department-scoped handler")
	assert.Equal(t, ieEventID, rev.gotEventID)
	assert.Equal(t, ieTenantID, rev.gotTenantID)
	assert.Equal(t, ieUserID, rev.gotUserID)
}

func TestInternalEvents_TenantMembershipRevoked_BadUserID(t *testing.T) {
	rev := &fakeMembershipRevoker{}
	w := postEvent(rev, envelope("MembershipRevoked", ieEventID.String(), ieTenantID.String(),
		map[string]string{"user_id": "not-a-uuid"}))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.False(t, rev.tenantCalled)
}

func TestInternalEvents_TenantMembershipRevoked_HandlerError(t *testing.T) {
	rev := &fakeMembershipRevoker{err: assert.AnError}
	w := postEvent(rev, envelope("MembershipRevoked", ieEventID.String(), ieTenantID.String(),
		map[string]string{"user_id": ieUserID.String()}))

	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"a transient failure must be retryable, not acknowledged")
}

func TestInternalEvents_UnhandledType_OK(t *testing.T) {
	rev := &fakeMembershipRevoker{}
	w := postEvent(rev, envelope("SomeOtherEvent", ieEventID.String(), ieTenantID.String(), map[string]any{}))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.False(t, rev.called, "unhandled types must not call the revoker")
	assert.False(t, rev.tenantCalled)
}

func TestInternalEvents_MalformedEnvelope_400(t *testing.T) {
	rev := &fakeMembershipRevoker{}
	r := internalRouter(rev)
	w := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/internal/events", bytes.NewReader([]byte(`not-json`)))
	httpReq.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, httpReq)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.False(t, rev.called)
}

func TestInternalEvents_InvalidPayload_400(t *testing.T) {
	rev := &fakeMembershipRevoker{}
	// type is known but payload user_id is not a UUID
	w := postEvent(rev, envelope("DepartmentMembershipRevoked", ieEventID.String(), ieTenantID.String(),
		map[string]string{"user_id": "not-a-uuid", "department_id": ieDeptID}))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.False(t, rev.called)
}

func TestInternalEvents_InvalidTenantID_400(t *testing.T) {
	rev := &fakeMembershipRevoker{}
	w := postEvent(rev, envelope("DepartmentMembershipRevoked", ieEventID.String(), "not-a-uuid",
		map[string]string{"user_id": ieUserID.String(), "department_id": ieDeptID}))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.False(t, rev.called)
}

func TestInternalEvents_ServiceError_500(t *testing.T) {
	rev := &fakeMembershipRevoker{err: errors.New("db failure")}
	w := postEvent(rev, envelope("DepartmentMembershipRevoked", ieEventID.String(), ieTenantID.String(),
		map[string]string{"user_id": ieUserID.String(), "department_id": ieDeptID}))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.True(t, rev.called)
}

func TestInternalEvents_InvalidEventID_400(t *testing.T) {
	rev := &fakeMembershipRevoker{}
	w := postEvent(rev, envelope("DepartmentMembershipRevoked", "not-a-uuid", ieTenantID.String(),
		map[string]string{"user_id": ieUserID.String(), "department_id": ieDeptID}))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.False(t, rev.called)
}

func TestInternalEvents_InvalidPayloadJSON_400(t *testing.T) {
	rev := &fakeMembershipRevoker{}
	// Payload is a JSON number, which cannot unmarshal into membershipRevokedPayload struct.
	w := postEvent(rev, envelope("DepartmentMembershipRevoked", ieEventID.String(), ieTenantID.String(), 123))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.False(t, rev.called)
}
