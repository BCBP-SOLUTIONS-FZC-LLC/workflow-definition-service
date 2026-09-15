package domain_test

import (
	"errors"
	"testing"

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
		{"port does not affect the host match", "http://api.internal:8080", allowed, false},
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
