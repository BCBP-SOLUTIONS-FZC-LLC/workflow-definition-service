package domain_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestValidateBaseURLHost_QueryOrFragment_IsRefused(t *testing.T) {
	allowed := []string{"api.internal"}
	tests := []struct {
		name    string
		baseURL string
	}{
		{"a query", "https://api.internal?x=secret"},
		{"a query after a path", "https://api.internal/v1?token=abc"},
		{"an empty query marker", "https://api.internal/v1?"},
		{"a fragment", "https://api.internal/v1#section"},
		{"an empty fragment marker", "https://api.internal/v1#"},
		{"a query and a fragment", "https://api.internal/v1?a=b#c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domain.ValidateBaseURLHost(tt.baseURL, allowed)

			assert.True(t, errors.Is(err, domain.ErrInvalidConnectorAliasInput))
			assert.ErrorContains(t, err, "query or fragment")
		})
	}
}
