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
		bpmncore.NodeTypeTimerBoundaryEvent,
		bpmncore.NodeTypeErrorBoundaryEvent,
		bpmncore.NodeTypeMessageBoundaryEvent,
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

// Validate() on event and gateway handlers are intentional no-ops (validation is
// handled by package-level validators). This test ensures they compile and return nil.
func TestNoOpValidate(t *testing.T) {
	if v := (StartEventHandler{}).Validate("", nil, nil, nil, nil, nil); v != nil {
		t.Errorf("StartEventHandler.Validate: expected nil, got %v", v)
	}
	if v := (EndEventHandler{}).Validate("", nil, nil, nil, nil, nil); v != nil {
		t.Errorf("EndEventHandler.Validate: expected nil, got %v", v)
	}
	if v := (TimerBoundaryHandler{}).Validate("", nil, nil, nil, nil, nil); v != nil {
		t.Errorf("TimerBoundaryHandler.Validate: expected nil, got %v", v)
	}
	if v := (ErrorBoundaryHandler{}).Validate("", nil, nil, nil, nil, nil); v != nil {
		t.Errorf("ErrorBoundaryHandler.Validate: expected nil, got %v", v)
	}
	if v := (MessageBoundaryHandler{}).Validate("", nil, nil, nil, nil, nil); v != nil {
		t.Errorf("MessageBoundaryHandler.Validate: expected nil, got %v", v)
	}
	if err := (ErrorBoundaryHandler{}).Compile("", nil); err != nil {
		t.Errorf("ErrorBoundaryHandler.Compile: expected nil, got %v", err)
	}
	if v := (ExclusiveGatewayHandler{}).Validate("", nil, nil, nil, nil, nil); v != nil {
		t.Errorf("ExclusiveGatewayHandler.Validate: expected nil, got %v", v)
	}
	if v := (ParallelGatewayHandler{}).Validate("", nil, nil, nil, nil, nil); v != nil {
		t.Errorf("ParallelGatewayHandler.Validate: expected nil, got %v", v)
	}
	if v := (SendTaskHandler{}).Validate("", nil, nil, nil, nil, nil); v != nil {
		t.Errorf("SendTaskHandler.Validate: expected nil, got %v", v)
	}
	if v := (ReceiveTaskHandler{}).Validate("", nil, nil, nil, nil, nil); v != nil {
		t.Errorf("ReceiveTaskHandler.Validate: expected nil, got %v", v)
	}
}
