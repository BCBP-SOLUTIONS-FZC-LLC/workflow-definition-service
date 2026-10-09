package domain

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ConnectorRestAlias struct {
	Alias        string
	Method       string
	BaseURL      string
	PathTemplate string
	Timeout      time.Duration
}

func (a ConnectorRestAlias) Validate() error {
	if a.Alias == "" {
		return fmt.Errorf("%w: alias is required", ErrInvalidConnectorAliasInput)
	}
	if !isValidHTTPMethod(a.Method) {
		return fmt.Errorf("%w: invalid method %q", ErrInvalidConnectorAliasInput, a.Method)
	}
	if a.BaseURL == "" {
		return fmt.Errorf("%w: baseURL is required", ErrInvalidConnectorAliasInput)
	}
	if a.PathTemplate == "" {
		return fmt.Errorf("%w: pathTemplate is required", ErrInvalidConnectorAliasInput)
	}
	return nil
}

func ValidateBaseURLHost(baseURL string, allowedHosts []string) error {
	if len(allowedHosts) == 0 {
		return fmt.Errorf("%w: no allowed connector alias hosts are configured; set CONNECTOR_ALIAS_ALLOWED_HOSTS before writing rest-call aliases", ErrInvalidConnectorAliasInput)
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("%w: baseURL is not a valid URL: %v", ErrInvalidConnectorAliasInput, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: baseURL scheme %q is not http or https", ErrInvalidConnectorAliasInput, u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("%w: baseURL must not carry userinfo credentials", ErrInvalidConnectorAliasInput)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return fmt.Errorf("%w: baseURL has no host", ErrInvalidConnectorAliasInput)
	}
	for _, allowed := range allowedHosts {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed == "" {
			continue
		}
		if strings.HasPrefix(allowed, ".") {
			if strings.HasSuffix(host, allowed) {
				return nil
			}
			continue
		}
		if host == allowed {
			return nil
		}
	}
	return fmt.Errorf("%w: baseURL host %q is not an allowed internal host", ErrInvalidConnectorAliasInput, host)
}

func isValidHTTPMethod(m string) bool {
	switch strings.ToUpper(m) {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}
