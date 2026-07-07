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
					if r.Method != http.MethodPost {
						t.Errorf("expected POST, got %s", r.Method)
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"eligible": true}) //nolint:errcheck
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
					json.NewEncoder(w).Encode(map[string]any{"eligible": true}) //nolint:errcheck
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
			name: "ineligible",
			makeHandler: func(_ *testing.T, _ *int) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusConflict)
				}
			},
			dept:  "dept-1",
			level: "approver",
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
					w.Write([]byte("not-json")) //nolint:errcheck
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
					if got := r.URL.Query().Get("department"); got != "dept&special=1" {
						t.Errorf("department = %q, want %q", got, "dept&special=1")
					}
					if got := r.URL.Query().Get("level"); got != "role=admin&x=y" {
						t.Errorf("level = %q, want %q", got, "role=admin&x=y")
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"eligible": true}) //nolint:errcheck
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
