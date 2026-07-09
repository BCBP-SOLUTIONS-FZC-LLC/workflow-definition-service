package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTryCheckEligibility_UnexpectedStatusCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := &MembershipClient{baseURL: srv.URL, client: srv.Client()}
	eligible, retryable, err := c.tryCheckEligibility(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for unexpected status code")
	}
	if retryable {
		t.Error("expected retryable=false for a non-5xx, non-409 status code")
	}
	if eligible {
		t.Error("expected eligible=false")
	}
}

func TestEligibilityBackoff_CapsAt500ms(t *testing.T) {
	if got := eligibilityBackoff(10); got != 500*time.Millisecond {
		t.Errorf("eligibilityBackoff(10) = %v, want 500ms cap", got)
	}
}
