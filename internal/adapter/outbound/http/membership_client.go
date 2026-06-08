package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

func NewMembershipClient(baseURL string) *MembershipClient {
	return &MembershipClient{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// CheckEligibility calls POST /tenants/:t/users/:u/eligibility?department=:dept&level=:role.
func (c *MembershipClient) CheckEligibility(
	ctx context.Context,
	tenantID, userID uuid.UUID,
	departmentID, role string,
) (bool, error) {
	url := fmt.Sprintf(
		"%s/tenants/%s/users/%s/eligibility?department=%s&level=%s",
		c.baseURL, tenantID, userID, departmentID, role,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return false, fmt.Errorf("build eligibility request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("%w: membership eligibility check: %w", domain.ErrUpstreamUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode == http.StatusConflict {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%w: membership returned %d", domain.ErrUpstreamUnavailable, resp.StatusCode)
	}

	var body struct {
		Eligible bool `json:"eligible"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, fmt.Errorf("decode eligibility response: %w", err)
	}
	return body.Eligible, nil
}
