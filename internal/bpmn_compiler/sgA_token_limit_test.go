package bpmn_compiler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sgATokenDocument(tokens int) string {
	pairs := (tokens - 2) / 2
	var b strings.Builder
	b.WriteString("<root>")
	for i := 0; i < pairs; i++ {
		b.WriteString("<a/>")
	}
	if (tokens-2)%2 == 1 {
		b.WriteString("<!---->")
	}
	b.WriteString("</root>")
	return b.String()
}

func TestCountTokens_ExactlyTheLimitPassesAndOneMoreIsRefused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		tokens  int
		wantErr bool
	}{
		{"exactly the maximum", maxTokens, false},
		{"one token over", maxTokens + 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := countTokens(context.Background(), sgATokenDocument(tc.tokens))

			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.True(t, errors.Is(err, errForbiddenXML), "err = %v", err)
			assert.Contains(t, err.Error(), "max 1000000")
		})
	}
}
