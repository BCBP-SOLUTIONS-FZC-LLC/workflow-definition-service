package bpmn_compiler

import (
	"testing"
)

const hashFixtureBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="Hash Test">
    <bpmn:startEvent id="Start_1" name="Start"/>
    <bpmn:endEvent   id="End_1"   name="End"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start_1" targetRef="End_1"/>
  </bpmn:process>
</bpmn:definitions>`

func TestCompiler_Hash(t *testing.T) {
	tests := []struct {
		name    string
		bpmn    string
		wantErr bool
	}{
		{
			name: "valid BPMN produces non-empty hash",
			bpmn: hashFixtureBPMN,
		},
		{
			name:    "malformed XML returns error",
			bpmn:    `<<not valid xml>>`,
			wantErr: true,
		},
		{
			name:    "empty input returns error",
			bpmn:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New()
			h, err := c.Hash(tt.bpmn)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if h == "" {
				t.Error("expected non-empty hash, got empty string")
			}

			// determinism: same input must always produce the same hash
			h2, err := c.Hash(tt.bpmn)
			if err != nil {
				t.Fatalf("second Hash() error: %v", err)
			}
			if h != h2 {
				t.Errorf("Hash is not deterministic: first=%q second=%q", h, h2)
			}
		})
	}
}
