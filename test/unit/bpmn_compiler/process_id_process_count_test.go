package bpmn_compiler_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestProcessID_ProcessCount_IsReportedUnderTheCodeThatNamesIt(t *testing.T) {
	cases := []struct {
		name      string
		bpmnXML   string
		wantCode  domain.BPMNErrorCode
		wantCount string
	}{
		{
			name:      "no process at all",
			bpmnXML:   sgADefinitionsOpen + "\n</bpmn:definitions>",
			wantCode:  domain.BPMNErrNoRunnableProcess,
			wantCount: "got 0",
		},
		{
			name:      "two processes",
			bpmnXML:   sgADefinitionsOpen + "\n" + sgAPool("P1", "X") + sgAPool("P2", "X") + "</bpmn:definitions>",
			wantCode:  domain.BPMNErrMultipleProcesses,
			wantCount: "got 2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := bpmn_compiler.New().ProcessID(context.Background(), tc.bpmnXML)

			var failed *domain.ValidationFailedError
			require.True(t, errors.As(err, &failed), "err = %v", err)
			assert.Empty(t, id)
			require.Len(t, failed.Errors, 1)
			assert.Equal(t, tc.wantCode, failed.Errors[0].Code)
			assert.Empty(t, failed.Errors[0].NodeID)
			assert.Equal(t, domain.SeverityError, failed.Errors[0].Severity)
			assert.Contains(t, failed.Errors[0].Message, tc.wantCount)
		})
	}
}
