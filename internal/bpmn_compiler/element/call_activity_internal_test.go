package element

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
)

func TestCallActivity_DeptIDFromIOMapping_NotFound(t *testing.T) {
	m := &bpmncore.ZeebeIOMapping{
		Inputs: []bpmncore.ZeebeIOVar{{Source: "x", Target: "clarificationId"}},
	}
	if got := deptIDFromIOMapping(m); got != "" {
		t.Errorf("deptIDFromIOMapping() = %q, want empty string", got)
	}
}

func TestCallActivity_ToIOMapping_EmptyAfterFiltering(t *testing.T) {
	m := &bpmncore.ZeebeIOMapping{
		Inputs: []bpmncore.ZeebeIOVar{
			{Source: "engineering", Target: "dept_id"},
			{Source: "{}", Target: "Depts"},
		},
	}
	if got := toIOMapping(m); got != nil {
		t.Errorf("toIOMapping() = %+v, want nil when only dept_id/Depts inputs are present", got)
	}
}
