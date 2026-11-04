package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeHealth(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()

	if err := probeHealth(context.Background(), srv.URL); err != nil {
		t.Fatalf("healthy server: %v", err)
	}
	status = http.StatusServiceUnavailable
	if err := probeHealth(context.Background(), srv.URL); err == nil {
		t.Fatal("a 503 from /healthz must fail the probe")
	}
	srv.Close()
	if err := probeHealth(context.Background(), srv.URL); err == nil {
		t.Fatal("an unreachable server must fail the probe")
	}
}

func TestHealthcheckBaseURL(t *testing.T) {
	t.Setenv("HTTP_PORT", "")
	if got := healthcheckBaseURL(); got != "http://127.0.0.1:8080" {
		t.Errorf("default = %q", got)
	}
	t.Setenv("HTTP_PORT", "9000")
	if got := healthcheckBaseURL(); got != "http://127.0.0.1:9000" {
		t.Errorf("HTTP_PORT=9000 gave %q", got)
	}
}

func TestIsHealthcheckInvocation(t *testing.T) {
	cases := map[string]bool{"-healthcheck": true, "migrate": false, "": false}
	for arg, want := range cases {
		args := []string{"/server"}
		if arg != "" {
			args = append(args, arg)
		}
		if got := isHealthcheckInvocation(args); got != want {
			t.Errorf("args %v: got %v, want %v", args, got, want)
		}
	}
}
