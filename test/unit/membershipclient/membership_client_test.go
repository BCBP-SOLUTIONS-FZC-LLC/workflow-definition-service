package membershipclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestMembershipClient_New(t *testing.T) {
	c := httpadapter.NewMembershipClient("http://example.com", 10*time.Second)
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestMembershipClient_CheckEligibility(t *testing.T) {
	tests := []struct {
		name         string
		makeHandler  func(t *testing.T, calls *int) http.HandlerFunc
		makeCtx      func(t *testing.T) context.Context
		closeServer  bool
		dept         string
		level        string
		wantEligible bool
		wantErr      bool
		wantSentinel error
		wantCalls    int
		checkElapsed func(t *testing.T, elapsed time.Duration)
	}{
		{
			name: "eligible",
			makeHandler: func(t *testing.T, _ *int) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						t.Errorf("expected GET, got %s", r.Method)
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"eligible": true}) //nolint:errcheck // a test handler has nowhere to report a failed write
				}
			},
			dept:         "dept-1",
			level:        "approver",
			wantEligible: true,
		},
		{
			name: "retries then succeeds",
			makeHandler: func(_ *testing.T, calls *int) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					*calls++
					if *calls < 3 {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"eligible": true}) //nolint:errcheck // a test handler has nowhere to report a failed write
				}
			},
			dept:         "dept-1",
			level:        "approver",
			wantEligible: true,
			wantCalls:    3,
		},
		{
			name: "retries exhausted",
			makeHandler: func(_ *testing.T, calls *int) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					*calls++
					w.WriteHeader(http.StatusBadGateway)
				}
			},
			dept:         "dept-1",
			level:        "approver",
			wantErr:      true,
			wantSentinel: domain.ErrUpstreamUnavailable,
			wantCalls:    3,
		},
		{
			name: "ineligible is an answer, not a failure",
			makeHandler: func(_ *testing.T, calls *int) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					*calls++
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"eligible": false, "reason": "level_insufficient", "role_level": "preparator"}) //nolint:errcheck // a test handler has nowhere to report a failed write
				}
			},
			dept:      "dept-1",
			level:     "approver",
			wantCalls: 1,
		},
		{
			name: "a 409 is not an answer",
			makeHandler: func(_ *testing.T, calls *int) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					*calls++
					w.WriteHeader(http.StatusConflict)
				}
			},
			dept:         "dept-1",
			level:        "approver",
			wantErr:      true,
			wantSentinel: domain.ErrUpstreamUnavailable,
			wantCalls:    1,
		},
		{
			name: "a 400 is a caller bug and is not retried",
			makeHandler: func(_ *testing.T, calls *int) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					*calls++
					w.Header().Set("Content-Type", "application/problem+json")
					w.WriteHeader(http.StatusBadRequest)
				}
			},
			dept:         "dept-1",
			level:        "approver",
			wantErr:      true,
			wantSentinel: domain.ErrUpstreamUnavailable,
			wantCalls:    1,
		},
		{
			name: "a 403 is not retried",
			makeHandler: func(_ *testing.T, calls *int) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					*calls++
					w.WriteHeader(http.StatusForbidden)
				}
			},
			dept:         "dept-1",
			level:        "approver",
			wantErr:      true,
			wantSentinel: domain.ErrUpstreamUnavailable,
			wantCalls:    1,
		},
		{
			name: "a 200 without the eligible flag is not an answer",
			makeHandler: func(_ *testing.T, _ *int) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"reason": "not_member"}) //nolint:errcheck // a test handler has nowhere to report a failed write
				}
			},
			dept:         "dept-1",
			level:        "approver",
			wantErr:      true,
			wantSentinel: domain.ErrUpstreamUnavailable,
		},
		{
			name: "non-OK status",
			makeHandler: func(_ *testing.T, _ *int) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			},
			dept:         "dept-1",
			level:        "approver",
			wantErr:      true,
			wantSentinel: domain.ErrUpstreamUnavailable,
		},
		{
			name: "decode error",
			makeHandler: func(_ *testing.T, _ *int) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte("not-json")) //nolint:errcheck // a test handler has nowhere to report a failed write
				}
			},
			dept:    "dept-1",
			level:   "approver",
			wantErr: true,
		},
		{
			name: "request error (server closed)",
			makeHandler: func(_ *testing.T, _ *int) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {}
			},
			closeServer: true,
			dept:        "dept-1",
			level:       "approver",
			wantErr:     true,
		},
		{
			name: "context cancelled during backoff short-circuits retry",
			makeHandler: func(_ *testing.T, calls *int) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					*calls++
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			},
			makeCtx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				time.AfterFunc(30*time.Millisecond, cancel)
				return ctx
			},
			dept:         "dept-1",
			level:        "approver",
			wantErr:      true,
			wantSentinel: domain.ErrUpstreamUnavailable,
			checkElapsed: func(t *testing.T, elapsed time.Duration) {
				if elapsed > 200*time.Millisecond {
					t.Errorf("expected context cancel to short-circuit retry; elapsed = %v", elapsed)
				}
			},
		},
		{
			name: "URL encodes params",
			makeHandler: func(t *testing.T, _ *int) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					if got := r.URL.Query().Get("department_id"); got != "dept&special=1" {
						t.Errorf("department_id = %q, want %q", got, "dept&special=1")
					}
					if got := r.URL.Query().Get("required_level"); got != "role=admin&x=y" {
						t.Errorf("required_level = %q, want %q", got, "role=admin&x=y")
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"eligible": true}) //nolint:errcheck // a test handler has nowhere to report a failed write
				}
			},
			dept:         "dept&special=1",
			level:        "role=admin&x=y",
			wantEligible: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(tt.makeHandler(t, &calls))
			if tt.closeServer {
				srv.Close()
			} else {
				defer srv.Close()
			}

			client := httpadapter.NewMembershipClient(srv.URL, 10*time.Second)
			ctx := context.Background()
			if tt.makeCtx != nil {
				ctx = tt.makeCtx(t)
			}
			start := time.Now()
			ok, err := client.CheckEligibility(ctx, uuid.New(), uuid.New(), tt.dept, tt.level)
			elapsed := time.Since(start)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantSentinel != nil && !errors.Is(err, tt.wantSentinel) {
					t.Errorf("expected %v, got %v", tt.wantSentinel, err)
				}
				if tt.checkElapsed != nil {
					tt.checkElapsed(t, elapsed)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if ok != tt.wantEligible {
					t.Errorf("eligible = %v, want %v", ok, tt.wantEligible)
				}
			}
			if tt.wantCalls > 0 && calls != tt.wantCalls {
				t.Errorf("calls = %d, want %d", calls, tt.wantCalls)
			}
		})
	}
}

func TestMembershipClient_CheckEligibility_RequestShape(t *testing.T) {
	tenantID, userID := uuid.New(), uuid.New()
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		json.NewEncoder(w).Encode(map[string]any{"eligible": true}) //nolint:errcheck // a test handler has nowhere to report a failed write
	}))
	defer srv.Close()

	client := httpadapter.NewMembershipClient(srv.URL+"/", 10*time.Second)
	if _, err := client.CheckEligibility(context.Background(), tenantID, userID, "dept-1", "reviewer"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantPath := "/api/v1/internal/tenants/" + tenantID.String() + "/members/" + userID.String() + "/eligibility"
	if got.URL.Path != wantPath {
		t.Errorf("path = %q, want %q", got.URL.Path, wantPath)
	}
	if v := got.URL.Query().Get("department_id"); v != "dept-1" {
		t.Errorf("department_id = %q, want dept-1", v)
	}
	if v := got.URL.Query().Get("required_level"); v != "reviewer" {
		t.Errorf("required_level = %q, want reviewer", v)
	}
	for header, want := range map[string]string{
		"x-user-id":      "iam-system",
		"x-tenant-roles": "iam-system",
		"x-tenant-id":    tenantID.String(),
	} {
		if v := got.Header.Get(header); v != want {
			t.Errorf("%s = %q, want %q", header, v, want)
		}
	}
}
