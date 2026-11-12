package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// The connector worker refuses these templates at startup (aliasconfig
// validatePathTemplate), so Definition must refuse them at write time.
func TestConnectorRestAlias_Validate_PathTemplateRefusedByTheConnectorLibrary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		path string
	}{
		{"double quote", `/x/"y`},
		{"less-than sign", "/x/<y"},
		{"greater-than sign", "/x/>y"},
		{"caret", "/x/^y"},
		{"backslash", `/x/\y`},
		{"backtick", "/x/`y"},
		{"pipe", "/a|b"},
		{"unterminated placeholder", "/x/{id"},
		{"unterminated placeholder in the query", "/x?a={id"},
		{"percent before a placeholder", "/x/%{id}"},
		{"bad escape in the path", "/x/%zz"},
		{"truncated escape in the path", "/x/%2"},
		{"bad escape in the query", "/x?q=%zz"},
		{"semicolon in the query", "/x?a=1;b=2"},
		{"semicolon after a placeholder in the query", "/x?a={id};b=2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := validRestAlias()
			a.PathTemplate = tc.path

			err := a.Validate()

			require.Error(t, err)
			assert.True(t, errors.Is(err, domain.ErrInvalidConnectorAliasInput))
		})
	}
}

func TestConnectorRestAlias_Validate_PathTemplateShapesTheLibraryAccepts(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"/x?a=1&b=2",
		"/x/%25",
		"/x/{id}/%2F{name}",
		"/x?q=a%20b",
		"/x/a;b",
		"/x/:id",
		"/x/(a)*",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			a := validRestAlias()
			a.PathTemplate = path

			assert.NoError(t, a.Validate())
		})
	}
}

func TestConnectorRestAlias_Validate_PathTemplateRawForbiddenCharacters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		path string
	}{
		{"raw at sign", "/x/a@b"},
		{"raw at sign right after the slash", "/@evil.example"},
		{"raw backslash", `/x\y`},
		{"leading double slash", "//evil.example/x"},
		{"raw space", "/x y"},
		{"raw tab", "/x\ty"},
		{"raw newline", "/x\ny"},
		{"raw control character", "/x\x01y"},
		{"raw delete character", "/x\x7fy"},
		{"raw dot dot", "/x/../y"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := validRestAlias()
			a.PathTemplate = tc.path

			err := a.Validate()

			require.Error(t, err)
			assert.True(t, errors.Is(err, domain.ErrInvalidConnectorAliasInput))
		})
	}
}

func TestConnectorRestAlias_Validate_DoubleSlashIsRefusedOnlyAsATemplatePrefix(t *testing.T) {
	t.Parallel()

	a := validRestAlias()
	a.PathTemplate = "/v1//x"

	assert.NoError(t, a.Validate(), "the connector library accepts // inside a template, so only a prefix is refused")
}

func TestConnectorRestAlias_Validate_PercentDecodingStopsAfterThreeRounds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"three rounds reach the dot", "/a/%25252e%25252e/b", true},
		{"four rounds are not followed", "/a/%2525252e%2525252e/b", false},
		{"two rounds reach an encoded at sign", "/a/%2540b", true},
		{"three rounds reach an encoded at sign", "/a/%252540b", true},
		{"four rounds are not followed to an encoded at sign", "/a/%25252540b", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := validRestAlias()
			a.PathTemplate = tc.path

			err := a.Validate()

			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestConnectorRestAlias_Validate_PathTemplateLengthBoundary(t *testing.T) {
	t.Parallel()

	atLimit := "/" + strings.Repeat("a", 2047)
	overLimit := "/" + strings.Repeat("a", 2048)

	a := validRestAlias()
	a.PathTemplate = atLimit
	assert.NoError(t, a.Validate(), "2048 bytes is allowed")

	a.PathTemplate = overLimit
	assert.ErrorContains(t, a.Validate(), "longer than 2048 bytes")
}

func TestValidateBaseURLHost_DoubleEncodedBasePath_IsRefused(t *testing.T) {
	t.Parallel()

	allowed := []string{"api.internal"}
	for _, tc := range []struct{ name, baseURL string }{
		{"double-encoded dot dot", "https://api.internal/api/%252e%252e/"},
		{"double-encoded dot dot, upper case", "https://api.internal/api/%252E%252E/x"},
		{"triple-encoded dot dot", "https://api.internal/%25252e%25252e/x"},
		{"double-encoded at sign", "https://api.internal/v1/a%2540b"},
		{"double-encoded backslash", "https://api.internal/v1%255Cx"},
		{"double-encoded double slash", "https://api.internal/v1%252f%252fx"},
		{"encoded dot then double-encoded dot", "https://api.internal/%2e%252e/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := domain.ValidateBaseURLHost(tc.baseURL, allowed)

			assert.True(t, errors.Is(err, domain.ErrInvalidConnectorAliasInput), "err = %v", err)
			assert.ErrorContains(t, err, "baseURL path")
		})
	}
}

func TestValidateBaseURLHost_BasePathPercentDecodingStopsAfterThreeRounds(t *testing.T) {
	t.Parallel()

	err := domain.ValidateBaseURLHost("https://api.internal/%2525252e%2525252e/x", []string{"api.internal"})

	assert.NoError(t, err, "four rounds are not followed, the same limit as the template")
}
