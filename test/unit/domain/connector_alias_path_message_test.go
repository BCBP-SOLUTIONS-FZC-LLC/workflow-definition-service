package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestConnectorRestAlias_Validate_PathLengthIsCountedInBytesAndSaysSo(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"ascii over the limit", "/" + strings.Repeat("a", 2048)},
		{"two-byte characters under 2048 characters but over 2048 bytes", "/" + strings.Repeat("é", 1024)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := validRestAlias()
			a.PathTemplate = tt.path

			err := a.Validate()

			assert.True(t, errors.Is(err, domain.ErrInvalidConnectorAliasInput))
			assert.ErrorContains(t, err, "pathTemplate is longer than 2048 bytes")
			assert.NotContains(t, err.Error(), "characters")
		})
	}
}
