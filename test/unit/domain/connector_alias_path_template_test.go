package domain_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestConnectorRestAlias_Validate_PathTemplateFragmentAndEncodedTraversal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"fragment marker", "/x#frag", true},
		{"fragment marker that would hide a fixed query", "/x#?role=user", true},
		{"fragment marker at the end", "/x#", true},
		{"encoded dot dot, lower case", "/a/%2e%2e/b", true},
		{"encoded dot dot, upper case", "/a/%2E%2E/b", true},
		{"encoded dot dot, mixed case", "/a/%2e%2E/b", true},
		{"dot then encoded dot", "/a/.%2e/b", true},
		{"encoded dot then dot", "/a/%2e./b", true},
		{"encoded dot dot at the end", "/a/%2e%2e", true},
		{"encoded dot dot in the fixed query", "/a?next=%2e%2e", true},
		{"double-encoded dot dot, lower case", "/a/%252e%252e/b", true},
		{"double-encoded dot dot, upper case", "/a/%252E%252E/b", true},
		{"triple-encoded dot dot", "/a/%25252e%25252e/b", true},
		{"encoded start of a network reference", "/%2f/evil.com", true},
		{"encoded at sign", "/%40evil.com", true},
		{"encoded backslash", "/a%5cb", true},
		{"encoded dot dot after a bad escape", "/%zz/%2e%2e", true},
		{"fixed query is legal", "/x?a=b", false},
		{"fixed query with several pairs is legal", "/x?a=b&c=d", false},
		{"placeholder is legal", "/v1/tenders/{id}/status", false},
		{"placeholder in the query is legal", "/v1/tenders?id={id}", false},
		{"encoded fragment marker is legal", "/x%23y", false},
		{"encoded space is legal", "/a%20b", false},
		{"encoded slash is legal", "/a%2Fb", false},
		{"a single encoded dot is legal", "/a/%2e/b", false},
		{"a dot inside a name is legal", "/v1.2/files/a.b", false},
		{"a bad escape in the path is refused as the worker refuses it", "/x/%zz", true},
		{"a percent before a placeholder is refused as the worker refuses it", "/x/%{id}", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := validRestAlias()
			a.PathTemplate = tc.path

			err := a.Validate()

			if tc.wantErr {
				require.Error(t, err)
				assert.True(t, errors.Is(err, domain.ErrInvalidConnectorAliasInput), "same sentinel, so the same 400 INVALID_INPUT as the base URL path rule")
				return
			}
			assert.NoError(t, err)
		})
	}
}
