package domain_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestValidateBaseURLHost_HostIsMatchedAsTextAndNeverResolvedToAnAddress(t *testing.T) {
	tests := []struct {
		name    string
		allowed []string
		baseURL string
		refused bool
	}{
		{"a listed loopback address is accepted", []string{"127.0.0.1"}, "http://127.0.0.1/api", false},
		{"a listed name that cannot resolve is accepted", []string{"nothing-resolves-here.invalid"}, "https://nothing-resolves-here.invalid/api", false},
		{"a suffix entry accepts any name under it", []string{".zone.invalid"}, "https://anything.zone.invalid/api", false},
		{"the short form of a listed address is refused", []string{"127.0.0.1"}, "http://127.1/api", true},
		{"the decimal form of a listed address is refused", []string{"127.0.0.1"}, "http://2130706433/api", true},
		{"an unlisted name is refused", []string{"nothing-resolves-here.invalid"}, "https://other.invalid/api", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domain.ValidateBaseURLHost(tt.baseURL, tt.allowed)

			if tt.refused {
				assert.True(t, errors.Is(err, domain.ErrInvalidConnectorAliasInput), "got: %v", err)
				return
			}
			assert.NoError(t, err)
		})
	}
}
