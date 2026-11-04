package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  newClient(timeout),
	}
}

const maxEligibilityAttempts = 3

func (c *MembershipClient) CheckEligibility(
	ctx context.Context,
	tenantID, userID uuid.UUID,
	departmentID, role string,
) (result domain.Eligibility, err error) {
	err = observed("membership", "check_eligibility", func() error {
		var callErr error
		result, callErr = c.checkEligibility(ctx, tenantID, userID, departmentID, role)
		return callErr
	})
	return result, err
}

func (c *MembershipClient) checkEligibility(
	ctx context.Context,
	tenantID, userID uuid.UUID,
	departmentID, role string,
) (domain.Eligibility, error) {
	params := url.Values{"department_id": {departmentID}, "required_level": {role}}
	rawURL := fmt.Sprintf("%s/api/v1/internal/tenants/%s/members/%s/eligibility?%s",
		c.baseURL, tenantID, userID, params.Encode())

	var lastErr error
	for attempt := 1; attempt <= maxEligibilityAttempts; attempt++ {
		result, retryable, err := c.tryCheckEligibility(ctx, rawURL, tenantID)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if attempt == maxEligibilityAttempts || !retryable {
			break
		}
		select {
		case <-ctx.Done():
			return domain.Eligibility{}, fmt.Errorf("%w: membership eligibility check: %w", domain.ErrUpstreamUnavailable, ctx.Err())
		case <-time.After(retryBackoff(attempt)):
		}
	}
	return domain.Eligibility{}, lastErr
}

func (c *MembershipClient) tryCheckEligibility(
	ctx context.Context, rawURL string, tenantID uuid.UUID,
) (result domain.Eligibility, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return domain.Eligibility{}, false, fmt.Errorf("build eligibility request: %w", err)
	}
	req.Header.Set("x-user-id", systemPrincipal)
	req.Header.Set("x-tenant-roles", systemPrincipal)
	req.Header.Set("x-tenant-id", tenantID.String())
	resp, err := c.client.Do(req)
	if err != nil {
		return domain.Eligibility{}, true, fmt.Errorf("%w: membership eligibility check: %w", domain.ErrUpstreamUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck // a failed close of a response body is not actionable

	switch {
	case resp.StatusCode >= 500:
		return domain.Eligibility{}, true, fmt.Errorf("%w: membership returned %d", domain.ErrUpstreamUnavailable, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return domain.Eligibility{}, false, fmt.Errorf("%w: membership returned %d", domain.ErrUpstreamUnavailable, resp.StatusCode)
	}

	var body struct {
		Eligible *bool  `json:"eligible"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(limitedBody(resp.Body)).Decode(&body); err != nil {
		return domain.Eligibility{}, false, fmt.Errorf("decode eligibility response: %w", err)
	}
	if body.Eligible == nil {
		return domain.Eligibility{}, false, fmt.Errorf("%w: membership eligibility response has no eligible flag", domain.ErrUpstreamUnavailable)
	}
	return domain.Eligibility{Eligible: *body.Eligible, Reason: body.Reason}, false, nil
}

func retryBackoff(attempt int) time.Duration {
	d := 50 * time.Millisecond << (attempt - 1)
	if d > 500*time.Millisecond {
		d = 500 * time.Millisecond
	}
	return d
}
