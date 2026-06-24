package element_test

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/element"
)

func TestDefaultElementHandlers_Registry(t *testing.T) {
	m := element.DefaultElementHandlers()
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

	// each registered handler's NodeType() must match its map key
	for nodeType, handler := range m {
		if got := handler.NodeType(); got != nodeType {
			t.Errorf("handler %T is registered under %q but NodeType() returns %q", handler, nodeType, got)
		}
	}
}
