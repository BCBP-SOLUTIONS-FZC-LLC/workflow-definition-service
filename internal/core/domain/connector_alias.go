package domain

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ConnectorRestAlias mirrors
// workflow-connectors/pkg/connectors/aliasconfig's Endpoint shape —
// duplicated here, not imported, because this repo's vendored
// workflow-connectors version predates that package's exported Validate.
// Switch to importing aliasconfig directly once workflow-connectors is
// published and this repo's go.mod is bumped.
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

// ValidateBaseURLHost rejects a rest-call alias whose BaseURL does not
// resolve to an allow-listed internal host.
//
// This is a credential-exfiltration control, not hygiene: restcall.Execute
// attaches the real x-internal-token and x-departments headers to every
// call it makes, so an alias pointing at an attacker-controlled host would
// hand that host a valid internal service credential. Nothing else in the
// path checks this — the alias registry is org-wide (no RLS) and writable
// through an internal endpoint.
//
// Fail-closed on an empty allowlist: an unconfigured deployment must not
// silently accept any host at all.
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
		// A leading dot is a suffix rule (".svc.cluster.local" matches any
		// host under it); anything else must match exactly, so "evil.com"
		// can never satisfy an allowlist entry of "api.internal".
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
