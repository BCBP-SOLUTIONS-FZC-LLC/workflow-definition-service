package bpmn_compiler_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestValidate_BoundaryEventDefinitionCount_IsReportedAsABoundaryAttachmentProblem(t *testing.T) {
	cases := []struct {
		name       string
		definition string
	}{
		{name: "no event definition", definition: ``},
		{
			name:       "timer and error definitions together",
			definition: timerP1D + `<bpmn:errorEventDefinition errorRef="Err_1"/>`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := minimalBPMN(`
    <bpmn:error id="Err_1" errorCode="E1"/>
    <bpmn:startEvent id="StartEvent_1"/>` + validTask("Task_1", "", "prep") + `
    <bpmn:boundaryEvent id="BE_1" attachedToRef="Task_1">` + tc.definition + `</bpmn:boundaryEvent>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:endEvent id="EndEvent_2"/>` +
				flowXML("F1", "StartEvent_1", "Task_1") + flowXML("F2", "Task_1", "EndEvent_1") + flowXML("F3", "BE_1", "EndEvent_2"))

			issues, err := bpmn_compiler.New().Validate(context.Background(), doc)

			require.NoError(t, err)
			var onBoundary []domain.BPMNErrorCode
			for _, issue := range issues {
				if issue.NodeID == "BE_1" && issue.Severity == domain.SeverityError {
					onBoundary = append(onBoundary, issue.Code)
				}
			}
			assert.Contains(t, onBoundary, domain.BPMNErrInvalidBoundaryAttachment)
			assert.NotContains(t, onBoundary, domain.BPMNErrMissingTaskDefinition)
		})
	}
}
