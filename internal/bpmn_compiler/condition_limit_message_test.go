package bpmn_compiler

import (
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
)

func TestValidateConditionExprs_LengthLimitMessageSaysBytes(t *testing.T) {
	t.Parallel()

	const prefixAndQuote = len(`decision == ""`)
	cases := []struct {
		name    string
		literal string
		total   int
	}{
		{"ASCII text over the limit", strings.Repeat("a", 4097-prefixAndQuote), 4097},
		{"two-byte characters under 4096 characters but over 4096 bytes", strings.Repeat("é", (4098-prefixAndQuote)/2), 4098},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc, g := w4bConditionProc(w4bConditionOfBytes(t, tc.literal, tc.total))

			errs := validator.ValidateConditionExprs(proc, g, nil)

			w4bCompare(t, w4bSig(errs), w4bWant("INVALID_CONDITION_EXPRESSION@F1"), errs)
			msg := errs[0].Message
			if !strings.Contains(msg, "4096 byte") {
				t.Errorf("message %q does not state the limit in bytes", msg)
			}
			if strings.Contains(msg, "character") {
				t.Errorf("message %q still counts in characters, but the rule counts bytes", msg)
			}
		})
	}
}
