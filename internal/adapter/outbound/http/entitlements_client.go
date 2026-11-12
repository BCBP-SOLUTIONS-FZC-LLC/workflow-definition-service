package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

const (
	// entitlementsIdleConnsPerHost matches the limit cache's cap of 32 concurrent reads, so a burst reuses connections.
	entitlementsIdleConnsPerHost = 32

	failTransport = "transport"
	failStatus    = "status"
	failResponse  = "response"
)

var _ port.EntitlementsReader = (*EntitlementsClient)(nil)

type EntitlementsClient struct {
	baseURL string
	client  *http.Client
}

func NewEntitlementsClient(baseURL string, timeout time.Duration) *EntitlementsClient {
	transport := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the standard library's default is a *http.Transport
	transport.MaxIdleConnsPerHost = entitlementsIdleConnsPerHost
	return &EntitlementsClient{baseURL: strings.TrimRight(baseURL, "/"), client: newClientOver(timeout, transport)}
}

func (c *EntitlementsClient) ReadWorkflowTemplateLimit(ctx context.Context, tenantID uuid.UUID) (limit *int64, err error) {
	err = observed("entitlements", "read_limit", func() error {
		var failure string
		var callErr error
		limit, failure, callErr = c.readWorkflowTemplateLimit(ctx, tenantID)
		if callErr != nil {
			observability.IncCounterVec(observability.EntitlementsCallErrorsTotal, failure)
		}
		return callErr
	})
	return limit, err
}

// readWorkflowTemplateLimit also names the failure class for the error counter.
func (c *EntitlementsClient) readWorkflowTemplateLimit(ctx context.Context, tenantID uuid.UUID) (*int64, string, error) {
	rawURL := fmt.Sprintf("%s/api/v1/internal/tenants/%s/entitlements", c.baseURL, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, failTransport, fmt.Errorf("%w: build entitlements request: %w", domain.ErrUpstreamUnavailable, err)
	}
	req.Header.Set("x-user-id", systemPrincipal)
	req.Header.Set("x-tenant-roles", systemPrincipal)
	req.Header.Set("x-tenant-id", tenantID.String())

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, failTransport, fmt.Errorf("%w: entitlements read: %w", domain.ErrUpstreamUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck // a failed close of a response body is not actionable

	if resp.StatusCode != http.StatusOK {
		return nil, failStatus, fmt.Errorf("%w: entitlements returned %d: %s", domain.ErrUpstreamUnavailable, resp.StatusCode, errorSnippet(resp.Body))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return nil, failTransport, fmt.Errorf("%w: read entitlements response: %w", domain.ErrUpstreamUnavailable, err)
	}
	if len(raw) > maxResponseBody {
		return nil, failResponse, fmt.Errorf("%w: entitlements response is larger than %d bytes", domain.ErrUpstreamUnavailable, maxResponseBody)
	}
	limit, err := decodeTemplateLimit(raw, tenantID)
	if err != nil {
		return nil, failResponse, err
	}
	return limit, "", nil
}

// decodeTemplateLimit refuses trailing data after the first JSON value, since Unmarshal reads the whole body.
func decodeTemplateLimit(raw []byte, tenantID uuid.UUID) (*int64, error) {
	var body struct {
		TenantID json.RawMessage `json:"tenant_id"`
		Limits   *struct {
			WorkflowTemplateLimit *struct {
				Value json.RawMessage `json:"value"`
			} `json:"workflow_template_limit"`
		} `json:"limits"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("%w: decode entitlements response: %w", domain.ErrUpstreamUnavailable, err)
	}
	if err := checkEntitlementsTenant(body.TenantID, tenantID); err != nil {
		return nil, err
	}
	if body.Limits == nil || body.Limits.WorkflowTemplateLimit == nil {
		return nil, fmt.Errorf("%w: entitlements response has no workflow_template_limit", domain.ErrUpstreamUnavailable)
	}
	value := body.Limits.WorkflowTemplateLimit.Value
	if value == nil {
		return nil, fmt.Errorf("%w: entitlements workflow_template_limit has no value", domain.ErrUpstreamUnavailable)
	}
	var limit *int64
	if err := json.Unmarshal(value, &limit); err != nil {
		return nil, fmt.Errorf("%w: entitlements workflow_template_limit is not an integer: %w", domain.ErrUpstreamUnavailable, err)
	}
	if limit != nil && *limit < 0 {
		return nil, fmt.Errorf("%w: entitlements workflow_template_limit is negative", domain.ErrUpstreamUnavailable)
	}
	return limit, nil
}

func checkEntitlementsTenant(raw json.RawMessage, want uuid.UUID) error {
	if raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("%w: entitlements response has no tenant_id", domain.ErrUpstreamUnavailable)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return fmt.Errorf("%w: entitlements tenant_id is not a string: %w", domain.ErrUpstreamUnavailable, err)
	}
	got, err := uuid.Parse(text)
	if err != nil {
		return fmt.Errorf("%w: entitlements tenant_id is not a uuid: %w", domain.ErrUpstreamUnavailable, err)
	}
	if got != want {
		return fmt.Errorf("%w: entitlements answered for tenant %s, not %s", domain.ErrUpstreamUnavailable, got, want)
	}
	return nil
}
