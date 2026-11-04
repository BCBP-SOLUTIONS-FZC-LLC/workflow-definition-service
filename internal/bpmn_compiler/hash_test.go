package bpmn_compiler

import (
	"context"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
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
		{
			name: "multiple processes produce a hash",
			bpmn: twoProcessBPMN("P1", "P2", "End_2"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New()
			h, err := c.Hash(context.Background(), tt.bpmn)
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
			h2, err := c.Hash(context.Background(), tt.bpmn)
			if err != nil {
				t.Fatalf("second Hash() error: %v", err)
			}
			if h != h2 {
				t.Errorf("Hash is not deterministic: first=%q second=%q", h, h2)
			}
		})
	}
}

// twoProcessBPMN is a main process plus the process it calls, as a bundled
// module or a same-file called process leaves them.
func twoProcessBPMN(firstID, secondID, calledEndID string) string {
	procs := map[string]string{
		"P1": `<bpmn:process id="P1" name="Main">
    <bpmn:startEvent id="Start_1"/>
    <bpmn:callActivity id="Call_1"/>
    <bpmn:endEvent id="End_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start_1" targetRef="Call_1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Call_1" targetRef="End_1"/>
  </bpmn:process>`,
		"P2": `<bpmn:process id="P2" name="Called">
    <bpmn:startEvent id="Start_2"/>
    <bpmn:endEvent id="` + calledEndID + `"/>
    <bpmn:sequenceFlow id="F3" sourceRef="Start_2" targetRef="` + calledEndID + `"/>
  </bpmn:process>`,
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  ` + procs[firstID] + `
  ` + procs[secondID] + `
</bpmn:definitions>`
}

func TestCompiler_Hash_MultiProcess(t *testing.T) {
	c := New()
	ctx := context.Background()
	hash := func(t *testing.T, bpmn string) string {
		t.Helper()
		h, err := c.Hash(ctx, bpmn)
		if err != nil {
			t.Fatalf("Hash() error: %v", err)
		}
		return h
	}

	base := hash(t, twoProcessBPMN("P1", "P2", "End_2"))

	t.Run("process order does not change the hash", func(t *testing.T) {
		if got := hash(t, twoProcessBPMN("P2", "P1", "End_2")); got != base {
			t.Errorf("reordered processes hashed differently: %q vs %q", got, base)
		}
	})
	t.Run("a change inside the called process changes the hash", func(t *testing.T) {
		if got := hash(t, twoProcessBPMN("P1", "P2", "End_2_changed")); got == base {
			t.Error("called-process body changed but the hash did not")
		}
	})
}

func TestCanonicalBundleHash_DuplicateProcessIDs_OrderIndependent(t *testing.T) {
	a := bpmncore.BPMNProcess{ID: "X", Name: "first"}
	b := bpmncore.BPMNProcess{ID: "X", Name: "second"}
	h1, err := canonicalBundleHash(&bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{a, b}})
	if err != nil {
		t.Fatalf("canonicalBundleHash: %v", err)
	}
	h2, err := canonicalBundleHash(&bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{b, a}})
	if err != nil {
		t.Fatalf("canonicalBundleHash: %v", err)
	}
	if h1 != h2 {
		t.Errorf("same processes in another order hashed differently: %q vs %q", h1, h2)
	}
}

func TestCompiler_Hash_SingleProcessIsItsCanonicalHash(t *testing.T) {
	defs, err := parse(context.Background(), hashFixtureBPMN)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want, err := canonicalHash(&defs.Processes[0])
	if err != nil {
		t.Fatalf("canonicalHash: %v", err)
	}
	got, err := New().Hash(context.Background(), hashFixtureBPMN)
	if err != nil {
		t.Fatalf("Hash() error: %v", err)
	}
	if got != want {
		t.Errorf("single-process hash = %q, want canonicalHash %q", got, want)
	}
}

func TestCanonicalHash_SortsZeebeProperties(t *testing.T) {
	// sortedZeebeItems' comparator only runs when there are 2+ items to compare;
	// exercise it directly via canonicalHash and confirm order-independence.
	newProc := func(items []bpmncore.ZeebeProperty) *bpmncore.BPMNProcess {
		return &bpmncore.BPMNProcess{
			UserTasks: []bpmncore.BPMNUserTask{{
				ID: "T1",
				ExtensionElements: bpmncore.BPMNExtensionElements{
					ZeebeProps: bpmncore.BPMNZeebeProperties{Items: items},
				},
			}},
		}
	}

	h1, err := canonicalHash(newProc([]bpmncore.ZeebeProperty{{Name: "zzz", Value: "2"}, {Name: "aaa", Value: "1"}}))
	if err != nil {
		t.Fatalf("canonicalHash() error: %v", err)
	}
	h2, err := canonicalHash(newProc([]bpmncore.ZeebeProperty{{Name: "aaa", Value: "1"}, {Name: "zzz", Value: "2"}}))
	if err != nil {
		t.Fatalf("canonicalHash() error: %v", err)
	}
	if h1 != h2 {
		t.Errorf("canonicalHash should be order-independent for zeebe:properties; h1=%q h2=%q", h1, h2)
	}
}
