package domain

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	MaxAliasTimeout    = time.Minute
	maxAliasNameLength = 64
	maxAliasPathLength = 2048
)

var aliasNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

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
	if len(a.Alias) > maxAliasNameLength || !aliasNamePattern.MatchString(a.Alias) {
		return fmt.Errorf("%w: alias must match %s and be at most %d characters", ErrInvalidConnectorAliasInput, aliasNamePattern, maxAliasNameLength)
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
	if err := validatePathTemplate(a.PathTemplate); err != nil {
		return err
	}
	if a.Timeout < 0 || a.Timeout > MaxAliasTimeout {
		return fmt.Errorf("%w: timeoutMs must be between 0 and %d", ErrInvalidConnectorAliasInput, MaxAliasTimeout.Milliseconds())
	}
	return nil
}

func validatePathTemplate(p string) error {
	switch {
	case len(p) > maxAliasPathLength:
		return fmt.Errorf("%w: pathTemplate is longer than %d characters", ErrInvalidConnectorAliasInput, maxAliasPathLength)
	case !strings.HasPrefix(p, "/"):
		return fmt.Errorf("%w: pathTemplate must start with /", ErrInvalidConnectorAliasInput)
	case strings.HasPrefix(p, "//"), strings.Contains(p, ".."), strings.Contains(p, "@"), strings.Contains(p, `\`):
		return fmt.Errorf("%w: pathTemplate must not contain //, .., @ or a backslash", ErrInvalidConnectorAliasInput)
	}
	for _, r := range p {
		if r <= ' ' || r == 0x7f {
			return fmt.Errorf("%w: pathTemplate must not contain whitespace or control characters", ErrInvalidConnectorAliasInput)
		}
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
	port := u.Port()
	for _, allowed := range allowedHosts {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed == "" {
			continue
		}
		allowedHost, allowedPort := allowed, ""
		if h, p, err := net.SplitHostPort(allowed); err == nil {
			allowedHost, allowedPort = h, p
		}
		if allowedPort != "" && allowedPort != port {
			continue
		}
		if strings.HasPrefix(allowedHost, ".") {
			if strings.HasSuffix(host, allowedHost) {
				return nil
			}
			continue
		}
		if host == allowedHost {
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
