package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

const healthcheckTimeout = 3 * time.Second

func isHealthcheckInvocation(args []string) bool {
	return len(args) > 1 && args[1] == "-healthcheck"
}

func healthcheckBaseURL() string {
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8080"
	}
	return "http://127.0.0.1:" + port
}

func probeHealth(ctx context.Context, baseURL string) error {
	ctx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
	if err != nil {
		return fmt.Errorf("build healthz request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("get healthz: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // a failed close of a fully read response body is not actionable
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %d", resp.StatusCode)
	}
	return nil
}

func runHealthcheck() int {
	if err := probeHealth(context.Background(), healthcheckBaseURL()); err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	return 0
}
