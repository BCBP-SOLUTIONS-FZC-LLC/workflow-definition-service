package element

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
)

func TestCallActivity_ToIOMapping_DropsTheInputsTheCompilerConsumes(t *testing.T) {
	m := &bpmncore.ZeebeIOMapping{
		Inputs: []bpmncore.ZeebeIOVar{
			{Source: "engineering", Target: "dept_id"},
			{Source: "{}", Target: "Depts"},
			{Source: "{}", Target: "Assignees"},
		},
	}
	if got := toIOMapping(m); got != nil {
		t.Errorf("toIOMapping() = %+v, want nil when only compiler-consumed inputs are present", got)
	}
}
