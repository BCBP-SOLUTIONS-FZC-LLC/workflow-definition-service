package bpmn_compiler

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestDefaultStageTypes(t *testing.T) {
	m := defaultStageTypes()
	cases := []struct {
		key      string
		activity string
	}{
		{"prep", "PrepActivity"},
		{"review", "ReviewActivity"},
		{"approve", "ApproveActivity"},
	}
	for _, tc := range cases {
		h, ok := m[tc.key]
		if !ok {
			t.Fatalf("expected stage type %q in defaultStageTypes", tc.key)
		}
		if h.ID() != tc.key {
			t.Errorf("ID() = %q; want %q", h.ID(), tc.key)
		}
		if h.ActivityName() != tc.activity {
			t.Errorf("ActivityName() = %q; want %q", h.ActivityName(), tc.activity)
		}
		if errs := h.ValidateProps("node", nil); errs != nil {
			t.Errorf("ValidateProps() = %v; want nil", errs)
		}
	}
}

// customElementHandler is a minimal ElementHandler for option testing.
type customElementHandler struct{}

func (customElementHandler) NodeType() bpmncore.FlowNodeType { return "custom" }
func (customElementHandler) Validate(string, *bpmncore.BPMNProcess, *bpmncore.Graph, *bpmncore.BPMNDefinitions, map[string]bpmncore.StageTypeHandler, map[bpmncore.FlowNodeType]bpmncore.ElementHandler) []domain.BPMNValidationError {
	return nil
}
func (customElementHandler) Compile(string, *bpmncore.CompileState) error { return nil }

func TestWithElementHandler_Override(t *testing.T) {
	c := NewCompiler(WithElementHandler(customElementHandler{}))
	h, ok := c.elements["custom"]
	if !ok {
		t.Fatal("WithElementHandler did not register custom element handler")
	}
	if h.NodeType() != "custom" {
		t.Errorf("NodeType() = %q; want %q", h.NodeType(), "custom")
	}
}

func TestWithStageType_Override(t *testing.T) {
	custom := customStage{}
	c := NewCompiler(WithStageType(custom))
	h, ok := c.stageTypes["custom"]
	if !ok {
		t.Fatal("WithStageType did not register custom stage type")
	}
	if h.ActivityName() != "CustomActivity" {
		t.Errorf("ActivityName() = %q; want %q", h.ActivityName(), "CustomActivity")
	}
}

// customStage is a minimal StageTypeHandler for option testing.
type customStage struct{}

func (customStage) ID() string           { return "custom" }
func (customStage) ActivityName() string { return "CustomActivity" }
func (customStage) ValidateProps(_ string, _ map[string]string) []domain.BPMNValidationError {
	return nil
}
