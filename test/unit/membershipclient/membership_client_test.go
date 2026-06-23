package membershipclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestMembershipClient_New(t *testing.T) {
	c := httpadapter.NewMembershipClient("http://example.com")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestMembershipClient_CheckEligibility_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"eligible": true}) //nolint:errcheck
	}))
	defer srv.Close()

	client := httpadapter.NewMembershipClient(srv.URL)
	ok, err := client.CheckEligibility(context.Background(), uuid.New(), uuid.New(), "dept-1", "approver")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected eligible=true")
	}
}

func TestMembershipClient_CheckEligibility_RetriesThenSucceeds(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 3 { // first two attempts fail transiently
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"eligible": true}) //nolint:errcheck
	}))
	defer srv.Close()

	client := httpadapter.NewMembershipClient(srv.URL)
	ok, err := client.CheckEligibility(context.Background(), uuid.New(), uuid.New(), "dept-1", "approver")
	if err != nil {
		t.Fatalf("unexpected error after retries: %v", err)
	}
	if !ok {
		t.Fatal("expected eligible=true")
	}
	if calls != 3 {
		t.Errorf("expected 3 attempts (2 retries), got %d", calls)
	}
}

func TestMembershipClient_CheckEligibility_RetriesExhausted(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway) // persistent 5xx
	}))
	defer srv.Close()

	client := httpadapter.NewMembershipClient(srv.URL)
	_, err := client.CheckEligibility(context.Background(), uuid.New(), uuid.New(), "dept-1", "approver")
	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Fatalf("expected ErrUpstreamUnavailable, got %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 attempts before giving up, got %d", calls)
	}
}

func TestMembershipClient_CheckEligibility_Ineligible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	client := httpadapter.NewMembershipClient(srv.URL)
	ok, err := client.CheckEligibility(context.Background(), uuid.New(), uuid.New(), "dept-1", "approver")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected eligible=false for 409")
	}
}

func TestMembershipClient_CheckEligibility_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := httpadapter.NewMembershipClient(srv.URL)
	_, err := client.CheckEligibility(context.Background(), uuid.New(), uuid.New(), "dept-1", "approver")
	if err == nil {
		t.Fatal("expected error for 503")
	}
	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Errorf("expected ErrUpstreamUnavailable, got %v", err)
	}
}

func TestMembershipClient_CheckEligibility_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not-json")) //nolint:errcheck
	}))
	defer srv.Close()

	client := httpadapter.NewMembershipClient(srv.URL)
	_, err := client.CheckEligibility(context.Background(), uuid.New(), uuid.New(), "dept-1", "approver")
	if err == nil {
		t.Fatal("expected decode error for malformed JSON")
	}
}

func TestMembershipClient_CheckEligibility_RequestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	client := httpadapter.NewMembershipClient(srv.URL)
	_, err := client.CheckEligibility(context.Background(), uuid.New(), uuid.New(), "dept-1", "approver")
	if err == nil {
		t.Fatal("expected error when server is closed")
	}
}

func TestMembershipClient_CheckEligibility_URLEncodesParams(t *testing.T) {
	var gotDept, gotLevel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotDept = r.URL.Query().Get("department")
		gotLevel = r.URL.Query().Get("level")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"eligible": true}) //nolint:errcheck
	}))
	defer srv.Close()

	client := httpadapter.NewMembershipClient(srv.URL)
	_, err := client.CheckEligibility(context.Background(), uuid.New(), uuid.New(), "dept&special=1", "role=admin&x=y")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotDept != "dept&special=1" {
		t.Errorf("expected department to be URL-decoded to 'dept&special=1', got %q", gotDept)
	}
	if gotLevel != "role=admin&x=y" {
		t.Errorf("expected level to be URL-decoded to 'role=admin&x=y', got %q", gotLevel)
	}
}
