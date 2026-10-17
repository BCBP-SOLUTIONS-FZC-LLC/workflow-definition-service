package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.PlanTableReader = (*CatalogClient)(nil)

const (
	internalPlansPath = "/api/v1/internal/plans"
	systemPrincipal   = "iam-system"
)

type CatalogClient struct {
	baseURL string
	client  *http.Client
}

func NewCatalogClient(baseURL string, timeout time.Duration) *CatalogClient {
	return &CatalogClient{baseURL: strings.TrimRight(baseURL, "/"), client: newClient(timeout)}
}

func (c *CatalogClient) ReadPlanTable(ctx context.Context) (table map[string]*int64, err error) {
	err = observed("catalog", "read_plan_table", func() error {
		var callErr error
		table, callErr = c.readPlanTable(ctx)
		return callErr
	})
	return table, err
}

func (c *CatalogClient) readPlanTable(ctx context.Context) (map[string]*int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+internalPlansPath, nil)
	if err != nil {
		return nil, fmt.Errorf("build plan table request: %w", err)
	}
	req.Header.Set("x-user-id", systemPrincipal)
	req.Header.Set("x-tenant-roles", systemPrincipal)
	req.Header.Set("x-tenant-id", uuid.Nil.String())

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: catalog plan table: %w", domain.ErrUpstreamUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: catalog returned %d: %s", domain.ErrUpstreamUnavailable, resp.StatusCode, errorSnippet(resp.Body))
	}

	var body struct {
		Plans []struct {
			Code                  string `json:"code"`
			WorkflowTemplateLimit *int64 `json:"workflow_template_limit"`
		} `json:"plans"`
	}
	if err := json.NewDecoder(limitedBody(resp.Body)).Decode(&body); err != nil {
		return nil, fmt.Errorf("%w: decode catalog plan table: %w", domain.ErrUpstreamUnavailable, err)
	}
	if len(body.Plans) == 0 {
		return nil, fmt.Errorf("%w: catalog returned no plans", domain.ErrUpstreamUnavailable)
	}
	table := make(map[string]*int64, len(body.Plans))
	for _, p := range body.Plans {
		table[strings.ToLower(p.Code)] = p.WorkflowTemplateLimit
	}
	return table, nil
}
