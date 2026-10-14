package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func validRestAlias() domain.ConnectorRestAlias {
	return domain.ConnectorRestAlias{
		Alias: "tender-get", Method: "GET",
		BaseURL: "http://tender-service.internal", PathTemplate: "/x",
	}
}

func TestConnectorRestAlias_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*domain.ConnectorRestAlias)
		wantErr bool
	}{
		{"valid", func(*domain.ConnectorRestAlias) {}, false},
		{"missing alias", func(a *domain.ConnectorRestAlias) { a.Alias = "" }, true},
		{"invalid method", func(a *domain.ConnectorRestAlias) { a.Method = "NOPE" }, true},
		{"lowercase method valid", func(a *domain.ConnectorRestAlias) { a.Method = "get" }, false},
		{"missing baseURL", func(a *domain.ConnectorRestAlias) { a.BaseURL = "" }, true},
		{"missing pathTemplate", func(a *domain.ConnectorRestAlias) { a.PathTemplate = "" }, true},
		{"alias with a slash", func(a *domain.ConnectorRestAlias) { a.Alias = "a/b" }, true},
		{"alias with a space", func(a *domain.ConnectorRestAlias) { a.Alias = "a b" }, true},
		{"alias with traversal", func(a *domain.ConnectorRestAlias) { a.Alias = ".." }, true},
		{"alias with a dot", func(a *domain.ConnectorRestAlias) { a.Alias = "a.b" }, true},
		{"alias at the length limit", func(a *domain.ConnectorRestAlias) { a.Alias = strings.Repeat("a", 64) }, false},
		{"alias over the length limit", func(a *domain.ConnectorRestAlias) { a.Alias = strings.Repeat("a", 65) }, true},
		{"path without a leading slash", func(a *domain.ConnectorRestAlias) { a.PathTemplate = "x" }, true},
		{"path that starts a network reference", func(a *domain.ConnectorRestAlias) { a.PathTemplate = "//evil.com/x" }, true},
		{"path with traversal", func(a *domain.ConnectorRestAlias) { a.PathTemplate = "/a/../b" }, true},
		{"path with userinfo marker", func(a *domain.ConnectorRestAlias) { a.PathTemplate = "/@evil.com" }, true},
		{"path with a backslash", func(a *domain.ConnectorRestAlias) { a.PathTemplate = "/a\\b" }, true},
		{"path with whitespace", func(a *domain.ConnectorRestAlias) { a.PathTemplate = "/a b" }, true},
		{"path with a control character", func(a *domain.ConnectorRestAlias) { a.PathTemplate = "/a\x00b" }, true},
		{"path with a template placeholder", func(a *domain.ConnectorRestAlias) { a.PathTemplate = "/v1/tenders/{id}/status" }, false},
		{"path over the length limit", func(a *domain.ConnectorRestAlias) { a.PathTemplate = "/" + strings.Repeat("a", 2048) }, true},
		{"timeout at the limit", func(a *domain.ConnectorRestAlias) { a.Timeout = domain.MaxAliasTimeout }, false},
		{"timeout over the limit", func(a *domain.ConnectorRestAlias) { a.Timeout = domain.MaxAliasTimeout + time.Millisecond }, true},
		{"negative timeout", func(a *domain.ConnectorRestAlias) { a.Timeout = -time.Second }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := validRestAlias()
			tt.mutate(&a)
			err := a.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.True(t, errors.Is(err, domain.ErrInvalidConnectorAliasInput))
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ValidateBaseURLHost is the control that stops a rest-call alias from
// pointing at an attacker-controlled host: dispatch attaches the real
// x-internal-token and x-departments headers to every call, and the alias
// registry is org-wide and writable through an internal endpoint, so an
// unchecked baseURL is a credential-exfiltration path.
func TestValidateBaseURLHost(t *testing.T) {
	allowed := []string{"api.internal", ".svc.cluster.local", "  ", "TENDER.Internal"}

	tests := []struct {
		name    string
		baseURL string
		hosts   []string
		wantErr bool
	}{
		{"exact host allowed", "https://api.internal", allowed, false},
		{"exact host is case-insensitive on both sides", "https://Tender.INTERNAL", allowed, false},
		{"an entry without a port allows any port", "http://api.internal:8080", allowed, false},
		{"an entry with a port requires it", "http://crm.internal:8443", []string{"crm.internal:8443"}, false},
		{"an entry with a port refuses another port", "http://crm.internal:22", []string{"crm.internal:8443"}, true},
		{"an entry with a port refuses no port", "http://crm.internal", []string{"crm.internal:8443"}, true},
		{"https default port matches an entry naming 443", "https://crm.internal", []string{"crm.internal:443"}, false},
		{"http default port matches an entry naming 80", "http://crm.internal", []string{"crm.internal:80"}, false},
		{"https default port does not match an entry naming 80", "https://crm.internal", []string{"crm.internal:80"}, true},
		{"a suffix rule with a port requires it", "http://a.svc:9000", []string{".svc:9000"}, false},
		{"a suffix rule with a port refuses another", "http://a.svc:9001", []string{".svc:9000"}, true},
		{"leading dot is a suffix rule", "http://tender.svc.cluster.local", allowed, false},
		{"suffix rule does not match a lookalike host", "http://evilsvc.cluster.local", allowed, true},
		{"unlisted host rejected", "https://evil.com", allowed, true},
		{"host that merely contains an allowed entry is rejected", "https://api.internal.evil.com", allowed, true},
		{"non-http scheme rejected", "file:///etc/passwd", allowed, true},
		{"userinfo credentials rejected", "https://user:pass@api.internal", allowed, true},
		{"no host rejected", "https:///just-a-path", allowed, true},
		{"unparseable url rejected", "http://a b.internal\x7f", allowed, true},
		// Fail-closed: an unconfigured deployment must reject every host
		// rather than silently trusting all of them.
		{"empty allowlist rejects an otherwise fine host", "https://api.internal", nil, true},
		{"allowlist of only blanks rejects", "https://api.internal", []string{"", "   "}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domain.ValidateBaseURLHost(tt.baseURL, tt.hosts)
			if tt.wantErr {
				assert.Error(t, err)
				assert.True(t, errors.Is(err, domain.ErrInvalidConnectorAliasInput))
				return
			}
			assert.NoError(t, err)
		})
	}
}
