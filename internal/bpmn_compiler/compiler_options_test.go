package bpmn_compiler

import (
	"testing"

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
