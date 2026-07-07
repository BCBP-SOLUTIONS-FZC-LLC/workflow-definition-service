package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.MembershipService = (*MembershipClient)(nil)

type MembershipClient struct {
	baseURL string
	client  *http.Client
}

func NewMembershipClient(baseURL string, timeout time.Duration) *MembershipClient {
	return &MembershipClient{
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
	}
}

// maxEligibilityAttempts bounds transient retries (1 initial try + retries) on
// the eligibility call; only transport errors and HTTP 5xx are retried.
const maxEligibilityAttempts = 3

func (c *MembershipClient) CheckEligibility(
	ctx context.Context,
	tenantID, userID uuid.UUID,
	departmentID, role string,
) (bool, error) {
	params := url.Values{"department": {departmentID}, "level": {role}}
	rawURL := fmt.Sprintf("%s/tenants/%s/users/%s/eligibility?%s",
		c.baseURL, tenantID, userID, params.Encode())

	var lastErr error
	for attempt := 1; attempt <= maxEligibilityAttempts; attempt++ {
		eligible, retryable, err := c.tryCheckEligibility(ctx, rawURL)
		if err == nil {
			return eligible, nil
		}
		lastErr = err
		if attempt == maxEligibilityAttempts || !retryable {
			break
		}
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("%w: membership eligibility check: %w", domain.ErrUpstreamUnavailable, ctx.Err())
		case <-time.After(eligibilityBackoff(attempt)):
		}
	}
	return false, lastErr
}

func (c *MembershipClient) tryCheckEligibility(ctx context.Context, rawURL string) (eligible, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, nil)
	if err != nil {
		return false, false, fmt.Errorf("build eligibility request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false, true, fmt.Errorf("%w: membership eligibility check: %w", domain.ErrUpstreamUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	switch {
	case resp.StatusCode == http.StatusConflict:
		return false, false, nil // not eligible
	case resp.StatusCode >= 500:
		return false, true, fmt.Errorf("%w: membership returned %d", domain.ErrUpstreamUnavailable, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return false, false, fmt.Errorf("%w: membership returned %d", domain.ErrUpstreamUnavailable, resp.StatusCode)
	}

	var body struct {
		Eligible bool `json:"eligible"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, false, fmt.Errorf("decode eligibility response: %w", err)
	}
	return body.Eligible, false, nil
}

func eligibilityBackoff(attempt int) time.Duration {
	d := 50 * time.Millisecond << (attempt - 1)
	if d > 500*time.Millisecond {
		d = 500 * time.Millisecond
	}
	return d
}
