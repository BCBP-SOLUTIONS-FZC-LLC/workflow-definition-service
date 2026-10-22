package bpmn_compiler

import "testing"

func TestStageType_ValidateProps_Prep(t *testing.T) {
	if errs := (prepStage{}).ValidateProps("n1", map[string]string{"anything": "goes"}); errs != nil {
		t.Errorf("ValidateProps() = %v, want nil", errs)
	}
}

func TestStageType_ValidateProps_Review(t *testing.T) {
	if errs := (reviewStage{}).ValidateProps("n1", map[string]string{"anything": "goes"}); errs != nil {
		t.Errorf("ValidateProps() = %v, want nil", errs)
	}
}

func TestStageType_ValidateProps_Approve(t *testing.T) {
	if errs := (approveStage{}).ValidateProps("n1", map[string]string{"anything": "goes"}); errs != nil {
		t.Errorf("ValidateProps() = %v, want nil", errs)
	}
}
