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

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.SecretsClient = (*SecretsClient)(nil)

type SecretsClient struct {
	addr   string
	token  string
	mount  string
	client *http.Client
}

func NewSecretsClient(addr, token, mount string, timeout time.Duration) *SecretsClient {
	return &SecretsClient{
		addr:   addr,
		token:  token,
		mount:  mount,
		client: &http.Client{Timeout: timeout},
	}
}

type openBaoWriteRequest struct {
	Data map[string]string `json:"data"`
}

func (c *SecretsClient) Write(ctx context.Context, path string, data map[string]string) error {
	body, err := json.Marshal(openBaoWriteRequest{Data: data})
	if err != nil {
		return fmt.Errorf("encode openbao write request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/%s/data/%s", strings.TrimRight(c.addr, "/"), c.mount, strings.TrimLeft(path, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build openbao write request: %w", err)
	}
	req.Header.Set("X-Vault-Token", c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: openbao write %q: %w", domain.ErrUpstreamUnavailable, path, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		return nil
	case http.StatusForbidden:
		return fmt.Errorf("%w: openbao write %q", domain.ErrUnauthorized, path)
	default:
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%w: openbao write %q: status %d: %s", domain.ErrUpstreamUnavailable, path, resp.StatusCode, string(respBody))
	}
}
