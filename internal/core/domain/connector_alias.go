package domain

import (
	"encoding/hex"
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
		return fmt.Errorf("%w: pathTemplate is longer than %d bytes", ErrInvalidConnectorAliasInput, maxAliasPathLength)
	case !strings.HasPrefix(p, "/"):
		return fmt.Errorf("%w: pathTemplate must start with /", ErrInvalidConnectorAliasInput)
	case strings.Contains(p, "#"):
		return fmt.Errorf("%w: pathTemplate must not contain # (a fragment is never sent; write a literal as %%23)", ErrInvalidConnectorAliasInput)
	}
	if err := (pathRules{subject: "pathTemplate"}).check(p); err != nil {
		return err
	}
	return checkConnectorLibraryTemplateRules(p)
}

// pathRules is the check a template and a base URL path share: no path
// traversal, userinfo or backslash marker and no whitespace or control
// character, in the raw text and in every percent-decoded form.
type pathRules struct {
	subject string
	// baseURL paths refuse // anywhere; a template only as a prefix, because the
	// connector library accepts // inside one.
	doubleSlashAnywhere bool
	// a base path refuses an encoded whitespace or control character too; a
	// template may carry %20.
	controlInDecodedForms bool
}

func (r pathRules) check(p string) error {
	for i, form := range append([]string{p}, percentDecodedForms(p)...) {
		if hasPathEscapeMarker(form) || r.hasDoubleSlash(form) {
			where := ""
			if i > 0 {
				where = ", in any percent-encoding"
			}
			return fmt.Errorf("%w: %s must not contain //, .., @ or a backslash%s", ErrInvalidConnectorAliasInput, r.subject, where)
		}
		if (i == 0 || r.controlInDecodedForms) && strings.IndexFunc(form, isSpaceOrControl) >= 0 {
			return fmt.Errorf("%w: %s must not contain whitespace or control characters", ErrInvalidConnectorAliasInput, r.subject)
		}
	}
	return nil
}

func (r pathRules) hasDoubleSlash(p string) bool {
	if r.doubleSlashAnywhere {
		return strings.Contains(p, "//")
	}
	return strings.HasPrefix(p, "//")
}

func isSpaceOrControl(r rune) bool { return r <= ' ' || r == 0x7f }

func hasPathEscapeMarker(p string) bool {
	return strings.Contains(p, "..") || strings.Contains(p, "@") || strings.Contains(p, `\`)
}

// checkConnectorLibraryTemplateRules repeats the rules the connector worker
// applies to every alias at startup (aliasconfig validatePathTemplate in
// workflow-connectors). A template it refuses stops alias loading for every
// tenant, so Definition must not store one.
func checkConnectorLibraryTemplateRules(tmpl string) error {
	var b strings.Builder
	for i := 0; i < len(tmpl); i++ {
		c := tmpl[i]
		if c == '{' {
			end := strings.IndexByte(tmpl[i:], '}')
			if end == -1 {
				return fmt.Errorf("%w: pathTemplate has an unterminated { placeholder", ErrInvalidConnectorAliasInput)
			}
			b.WriteByte('x') // not a hex digit, so %{id} stays a bad escape
			i += end
			continue
		}
		if strings.IndexByte(`"<>\^`+"`|", c) >= 0 {
			return fmt.Errorf("%w: pathTemplate contains %q, which is not valid in a URL (escape it)", ErrInvalidConnectorAliasInput, c)
		}
		b.WriteByte(c)
	}
	path, query, hasQuery := strings.Cut(b.String(), "?")
	if _, err := url.PathUnescape(path); err != nil {
		return fmt.Errorf("%w: pathTemplate path has an invalid escape (write a literal %% as %%25)", ErrInvalidConnectorAliasInput)
	}
	if !hasQuery {
		return nil
	}
	if strings.Contains(query, ";") {
		return fmt.Errorf("%w: pathTemplate query must not contain ; (separate parameters with &, write a literal as %%3B)", ErrInvalidConnectorAliasInput)
	}
	if _, err := url.ParseQuery(query); err != nil {
		return fmt.Errorf("%w: pathTemplate query is not a valid query string", ErrInvalidConnectorAliasInput)
	}
	return nil
}

const maxPercentDecodeRounds = 3

var percentEscape = regexp.MustCompile(`%[0-9a-fA-F]{2}`)

func percentDecodedForms(p string) []string {
	var forms []string
	for range maxPercentDecodeRounds {
		decoded := percentEscape.ReplaceAllStringFunc(p, func(esc string) string {
			b, _ := hex.DecodeString(esc[1:])
			return string(b)
		})
		if decoded == p {
			break
		}
		forms = append(forms, decoded)
		p = decoded
	}
	return forms
}

func validateBasePath(u *url.URL) error {
	return pathRules{subject: "baseURL path", doubleSlashAnywhere: true, controlInDecodedForms: true}.check(u.EscapedPath())
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
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(baseURL, "#") {
		return fmt.Errorf("%w: baseURL must not carry a query or fragment", ErrInvalidConnectorAliasInput)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return fmt.Errorf("%w: baseURL has no host", ErrInvalidConnectorAliasInput)
	}
	if err := validateBasePath(u); err != nil {
		return err
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
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
