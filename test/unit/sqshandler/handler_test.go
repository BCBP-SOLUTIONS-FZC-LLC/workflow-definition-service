package sqshandler_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"

	sqsadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/sqs"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

// ── fakes ────────────────────────────────────────────────────────────────────

type fakeLogger struct{}

func (f *fakeLogger) Info(msg string, fields map[string]any)  {}
func (f *fakeLogger) Error(msg string, fields map[string]any) {}
func (f *fakeLogger) Fatal(msg string, fields map[string]any) {}
func (f *fakeLogger) Warn(msg string, fields map[string]any)  {}
func (f *fakeLogger) Debug(msg string, fields map[string]any) {}

var _ port.Logger = (*fakeLogger)(nil)

type fakeMembershipRevoker struct {
	called bool
	err    error

	gotEventID  uuid.UUID
	gotTenantID uuid.UUID
	gotUserID   uuid.UUID
	gotDeptID   string
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

// ── helpers ───────────────────────────────────────────────────────────────────

var (
	hEventID  = uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	hTenantID = uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	hUserID   = uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	hDeptID   = "dept-finance"
)

func newEnvelope(eventType, eventID, tenantID string, payload any) events.Envelope[json.RawMessage] {
	raw, _ := json.Marshal(payload)
	return events.Envelope[json.RawMessage]{
		ID:       eventID,
		Type:     eventType,
		TenantID: tenantID,
		Payload:  raw,
	}
}

func newHandler(revoker *fakeMembershipRevoker) func(context.Context, events.Envelope[json.RawMessage]) error {
	return sqsadapter.NewHandler(&fakeLogger{}, revoker)
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestDispatch_UnhandledEventType(t *testing.T) {
	revoker := &fakeMembershipRevoker{}
	h := newHandler(revoker)

	err := h(context.Background(), newEnvelope("SomeOtherEvent", hEventID.String(), hTenantID.String(), map[string]any{}))

	if err != nil {
		t.Fatalf("expected nil for unhandled type, got %v", err)
	}
	if revoker.called {
		t.Error("HandleMembershipRevoked must not be called for unhandled event types")
	}
}

func TestDispatch_MalformedPayload(t *testing.T) {
	revoker := &fakeMembershipRevoker{}
	h := sqsadapter.NewHandler(&fakeLogger{}, revoker)

	env := events.Envelope[json.RawMessage]{
		ID:       hEventID.String(),
		Type:     "DepartmentMembershipRevoked",
		TenantID: hTenantID.String(),
		Payload:  json.RawMessage(`not-valid-json`),
	}

	err := h(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for malformed payload, got nil")
	}
	if revoker.called {
		t.Error("HandleMembershipRevoked must not be called on unmarshal error")
	}
}

func TestDispatch_InvalidEventID(t *testing.T) {
	revoker := &fakeMembershipRevoker{}
	h := newHandler(revoker)

	env := events.Envelope[json.RawMessage]{
		ID:       "not-a-uuid",
		Type:     "DepartmentMembershipRevoked",
		TenantID: hTenantID.String(),
		Payload:  mustMarshal(map[string]string{"user_id": hUserID.String(), "department_id": hDeptID}),
	}

	err := h(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for invalid event ID, got nil")
	}
	if revoker.called {
		t.Error("HandleMembershipRevoked must not be called on invalid event ID")
	}
}

func TestDispatch_InvalidTenantID(t *testing.T) {
	revoker := &fakeMembershipRevoker{}
	h := newHandler(revoker)

	env := events.Envelope[json.RawMessage]{
		ID:       hEventID.String(),
		Type:     "DepartmentMembershipRevoked",
		TenantID: "not-a-uuid",
		Payload:  mustMarshal(map[string]string{"user_id": hUserID.String(), "department_id": hDeptID}),
	}

	err := h(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for invalid tenant_id, got nil")
	}
	if revoker.called {
		t.Error("HandleMembershipRevoked must not be called on invalid tenant_id")
	}
}

func TestDispatch_InvalidUserID(t *testing.T) {
	revoker := &fakeMembershipRevoker{}
	h := newHandler(revoker)

	env := events.Envelope[json.RawMessage]{
		ID:       hEventID.String(),
		Type:     "DepartmentMembershipRevoked",
		TenantID: hTenantID.String(),
		Payload:  mustMarshal(map[string]string{"user_id": "not-a-uuid", "department_id": hDeptID}),
	}

	err := h(context.Background(), env)
	if err == nil {
		t.Fatal("expected error for invalid user_id, got nil")
	}
	if revoker.called {
		t.Error("HandleMembershipRevoked must not be called on invalid user_id")
	}
}

func TestDispatch_HappyPath(t *testing.T) {
	revoker := &fakeMembershipRevoker{}
	h := newHandler(revoker)

	err := h(context.Background(), newEnvelope(
		"DepartmentMembershipRevoked",
		hEventID.String(),
		hTenantID.String(),
		map[string]string{"user_id": hUserID.String(), "department_id": hDeptID, "role": "reviewer"},
	))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !revoker.called {
		t.Fatal("HandleMembershipRevoked was not called")
	}
	if revoker.gotEventID != hEventID {
		t.Errorf("event_id: got %s, want %s", revoker.gotEventID, hEventID)
	}
	if revoker.gotTenantID != hTenantID {
		t.Errorf("tenant_id: got %s, want %s", revoker.gotTenantID, hTenantID)
	}
	if revoker.gotUserID != hUserID {
		t.Errorf("user_id: got %s, want %s", revoker.gotUserID, hUserID)
	}
	if revoker.gotDeptID != hDeptID {
		t.Errorf("department_id: got %s, want %s", revoker.gotDeptID, hDeptID)
	}
}

func TestDispatch_ServiceError(t *testing.T) {
	revoker := &fakeMembershipRevoker{err: errors.New("db failure")}
	h := newHandler(revoker)

	err := h(context.Background(), newEnvelope(
		"DepartmentMembershipRevoked",
		hEventID.String(),
		hTenantID.String(),
		map[string]string{"user_id": hUserID.String(), "department_id": hDeptID},
	))

	if err == nil {
		t.Fatal("expected error propagation from service, got nil")
	}
}

// ── test helpers ──────────────────────────────────────────────────────────────

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
