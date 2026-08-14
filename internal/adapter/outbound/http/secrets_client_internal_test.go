package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestSecretsClient_Write(t *testing.T) {
	var gotToken, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Vault-Token")
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &SecretsClient{addr: srv.URL, token: "test-token", mount: "secret", client: srv.Client()}
	err := c.Write(context.Background(), "connectors/tenant-a/send-email", map[string]string{"apiKey": "sg-live-abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotToken != "test-token" {
		t.Errorf("X-Vault-Token = %q, want test-token", gotToken)
	}
	if gotPath != "/v1/secret/data/connectors/tenant-a/send-email" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestSecretsClient_Write_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := &SecretsClient{addr: srv.URL, token: "bad-token", mount: "secret", client: srv.Client()}
	err := c.Write(context.Background(), "some/path", map[string]string{"k": "v"})
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestSecretsClient_Write_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := &SecretsClient{addr: srv.URL, token: "test-token", mount: "secret", client: srv.Client()}
	err := c.Write(context.Background(), "some/path", map[string]string{"k": "v"})
	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Fatalf("expected ErrUpstreamUnavailable, got %v", err)
	}
}
