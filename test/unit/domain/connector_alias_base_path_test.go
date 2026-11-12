package domain_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestValidateBaseURLHost_BasePathWithTraversalOrSmuggledParts_IsRefused(t *testing.T) {
	allowed := []string{"api.internal"}
	tests := []struct {
		name    string
		baseURL string
	}{
		{"dot-dot segment", "https://api.internal/../x"},
		{"dot-dot after a segment", "https://api.internal/v1/../x"},
		{"dot-dot at the end", "https://api.internal/v1/.."},
		{"dot-dot inside a segment", "https://api.internal/v1..x"},
		{"percent-encoded dot-dot", "https://api.internal/%2e%2e/x"},
		{"mixed-case percent-encoded dot-dot", "https://api.internal/%2E%2e/x"},
		{"double slash at the start", "https://api.internal//x"},
		{"double slash inside", "https://api.internal/v1//x"},
		{"percent-encoded double slash", "https://api.internal/v1%2f%2fx"},
		{"at sign", "https://api.internal/v1/a@b"},
		{"percent-encoded at sign", "https://api.internal/v1/a%40b"},
		{"backslash", `https://api.internal/v1\x`},
		{"percent-encoded backslash", "https://api.internal/v1%5Cx"},
		{"space", "https://api.internal/v1 x"},
		{"percent-encoded space", "https://api.internal/v1%20x"},
		{"percent-encoded control character", "https://api.internal/v1%00x"},
		{"percent-encoded newline", "https://api.internal/v1%0Ax"},
		{"del character", "https://api.internal/v1%7Fx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domain.ValidateBaseURLHost(tt.baseURL, allowed)

			assert.True(t, errors.Is(err, domain.ErrInvalidConnectorAliasInput), "err = %v", err)
			assert.ErrorContains(t, err, "baseURL path")
		})
	}
}

func TestValidateBaseURLHost_OrdinaryBasePath_IsAccepted(t *testing.T) {
	allowed := []string{"api.internal"}
	for _, baseURL := range []string{
		"https://api.internal",
		"https://api.internal/",
		"https://api.internal/v2",
		"https://api.internal/v2/",
		"https://api.internal/api/v1.2/tenders",
		"https://api.internal/a-b_c/d~e",
		"http://api.internal:8080/svc",
	} {
		t.Run(baseURL, func(t *testing.T) {
			assert.NoError(t, domain.ValidateBaseURLHost(baseURL, allowed))
		})
	}
}
