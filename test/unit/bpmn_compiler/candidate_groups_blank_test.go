package bpmn_compiler_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestValidate_CandidateGroupsThatAreBlank_AreReportedAsEmpty(t *testing.T) {
	cases := []struct {
		name   string
		groups string
		want   []domain.BPMNErrorCode
	}{
		{name: "empty value", groups: ``, want: []domain.BPMNErrorCode{domain.BPMNErrCandidateGroupsEmpty}},
		{name: "spaces only", groups: `   `, want: []domain.BPMNErrorCode{domain.BPMNErrCandidateGroupsEmpty}},
		{name: "tab and newline only", groups: "&#9;&#10;", want: []domain.BPMNErrorCode{domain.BPMNErrCandidateGroupsEmpty}},
		{name: "unknown role", groups: `janitor`, want: []domain.BPMNErrorCode{domain.BPMNErrCandidateGroupsInvalid}},
		{name: "valid role", groups: `preparator`, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := `<bpmn:userTask id="Task_1" name="Task_1">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="` + tc.groups + `" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>`
			doc := minimalBPMN(`<bpmn:startEvent id="StartEvent_1"/>` + task + `<bpmn:endEvent id="EndEvent_1"/>` +
				flowXML("F1", "StartEvent_1", "Task_1") + flowXML("F2", "Task_1", "EndEvent_1"))

			issues, err := bpmn_compiler.New().Validate(context.Background(), doc)

			require.NoError(t, err)
			var got []domain.BPMNErrorCode
			for _, issue := range issues {
				if issue.Code == domain.BPMNErrCandidateGroupsEmpty || issue.Code == domain.BPMNErrCandidateGroupsInvalid {
					assert.Equal(t, "Task_1", issue.NodeID)
					assert.Equal(t, domain.SeverityError, issue.Severity)
					got = append(got, issue.Code)
				}
			}
			assert.Equal(t, tc.want, got)
		})
	}
}
