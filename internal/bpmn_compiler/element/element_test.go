package element

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
)

func TestDefaultElementHandlers_Registry(t *testing.T) {
	m := DefaultElementHandlers()
	if m == nil {
		t.Fatal("DefaultElementHandlers() returned nil")
	}

	wantTypes := []bpmncore.FlowNodeType{
		bpmncore.NodeTypeStartEvent,
		bpmncore.NodeTypeEndEvent,
		bpmncore.NodeTypeUserTask,
		bpmncore.NodeTypeSubProcess,
		bpmncore.NodeTypeExclusiveGateway,
		bpmncore.NodeTypeParallelGateway,
		bpmncore.NodeTypeBoundaryEvent,
	}
	for _, nt := range wantTypes {
		if _, ok := m[nt]; !ok {
			t.Errorf("handler missing for NodeType %q", nt)
		}
	}

	for nodeType, handler := range m {
		if got := handler.NodeType(); got != nodeType {
			t.Errorf("handler %T is registered under %q but NodeType() returns %q", handler, nodeType, got)
		}
	}
}
