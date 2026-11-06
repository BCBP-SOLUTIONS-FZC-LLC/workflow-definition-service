package bpmn_compiler_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestValidateForPublish_StructuralError_SkipsPublishOnlyChecks(t *testing.T) {
	t.Parallel()

	withoutStart := strings.Replace(lanelessRoot(false), "    <bpmn:startEvent id=\"Start_1\"/>\n", "", 1)
	withoutStart = strings.Replace(withoutStart, flowXML("F1", "Start_1", "Task_A"), "", 1)

	cases := []struct {
		name        string
		bpmnXML     string
		wantBlocked domain.BPMNErrorCode
		wantAbsent  domain.BPMNErrorCode
	}{
		{
			name:        "a missing start event hides the missing-lane issue",
			bpmnXML:     withoutStart,
			wantBlocked: domain.BPMNErrNoStartEvent,
			wantAbsent:  domain.BPMNErrMissingDeptID,
		},
		{
			name:        "a sound structure reaches the publish-only lane check",
			bpmnXML:     lanelessRoot(false),
			wantBlocked: domain.BPMNErrMissingDeptID,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := bpmn_compiler.New()

			errs, err := c.ValidateForPublish(context.Background(), tc.bpmnXML)

			require.NoError(t, err)
			assert.NotNil(t, findCode(blockingErrors(errs), tc.wantBlocked), "issues = %+v", errs)
			if tc.wantAbsent != "" {
				assert.Nil(t, findCode(errs, tc.wantAbsent), "issues = %+v", errs)
				plain, err := c.Validate(context.Background(), tc.bpmnXML)
				require.NoError(t, err)
				assert.Equal(t, plain, errs, "no publish-time check added anything")
			}
		})
	}
}
