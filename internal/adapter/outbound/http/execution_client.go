package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

const (
	maxExecutionAttempts = 3
	internalTokenHeader  = "x-internal-token"
)

var _ port.ExecutionService = (*ExecutionClient)(nil)

// ExecutionClient calls the Execution Service's internal API, specified in
// api/contracts/execution-internal-openapi.yaml (definition LLD §3.4.2).
// Every failure is domain.ErrUpstreamUnavailable.
type ExecutionClient struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewExecutionClient bounds each attempt by timeout; token is the Execution
// Service's INTERNAL_API_TOKEN.
func NewExecutionClient(baseURL, token string, timeout time.Duration) *ExecutionClient {
	return &ExecutionClient{baseURL: baseURL, token: token, client: &http.Client{Timeout: timeout}}
}

func (c *ExecutionClient) CheckActiveInstances(ctx context.Context, tenantID, workflowID uuid.UUID) (bool, int32, error) {
	rawURL := c.baseURL + "/api/v1/internal/workflows/" + workflowID.String() + "/active-instances?" +
		url.Values{"tenant_id": {tenantID.String()}}.Encode()
	var out struct {
		HasActive *bool `json:"has_active"`
		Count     int32 `json:"count"`
	}
	if err := c.call(ctx, "check active instances", http.MethodGet, rawURL, nil, http.StatusOK, &out); err != nil {
		return false, 0, err
	}
	if out.HasActive == nil {
		return false, 0, fmt.Errorf("%w: check active instances: response has no has_active", domain.ErrUpstreamUnavailable)
	}
	return *out.HasActive, out.Count, nil
}

func (c *ExecutionClient) PauseUserTasks(ctx context.Context, tenantID, userID uuid.UUID) error {
	body, err := json.Marshal(map[string]string{"tenant_id": tenantID.String()})
	if err != nil {
		return fmt.Errorf("%w: pause user tasks: %w", domain.ErrUpstreamUnavailable, err)
	}
	rawURL := c.baseURL + "/api/v1/internal/users/" + userID.String() + "/pause-tasks"
	return c.call(ctx, "pause user tasks", http.MethodPost, rawURL, body, http.StatusNoContent, nil)
}

// call makes up to maxExecutionAttempts attempts, retrying a transport error
// or a 5xx, and decodes a want-status body into out when out is non-nil.
func (c *ExecutionClient) call(ctx context.Context, op, method, rawURL string, body []byte, want int, out any) error {
	var lastErr error
	for attempt := 1; attempt <= maxExecutionAttempts; attempt++ {
		retryable, err := c.try(ctx, method, rawURL, body, want, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable || attempt == maxExecutionAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %s: %w", domain.ErrUpstreamUnavailable, op, ctx.Err())
		case <-time.After(retryBackoff(attempt)):
		}
	}
	return fmt.Errorf("%w: %s: %w", domain.ErrUpstreamUnavailable, op, lastErr)
}

func (c *ExecutionClient) try(ctx context.Context, method, rawURL string, body []byte, want int, out any) (retryable bool, err error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return false, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set(internalTokenHeader, c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return ctx.Err() == nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	switch {
	case resp.StatusCode >= 500:
		return true, fmt.Errorf("status %d", resp.StatusCode)
	case resp.StatusCode != want:
		return false, fmt.Errorf("status %d", resp.StatusCode)
	}
	if out == nil {
		return false, nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return false, fmt.Errorf("decode response: %w", err)
	}
	return false, nil
}
