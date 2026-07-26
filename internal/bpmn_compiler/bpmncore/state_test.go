package bpmncore

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"
)

func TestPatchStep(t *testing.T) {
	s := newMinimalState()
	s.AppendStep(dsl.ExecutionStep{Sequential: []string{"a"}})
	s.AppendStep(dsl.ExecutionStep{Sequential: []string{"b"}})

	s.PatchStep(0, func(step *dsl.ExecutionStep) {
		step.Sequential = append(step.Sequential, "x")
	})

	steps := s.CollectedSteps()
	if len(steps[0].Sequential) != 2 || steps[0].Sequential[1] != "x" {
		t.Errorf("expected index 0 patched; got %v", steps[0].Sequential)
	}
	if len(steps[1].Sequential) != 1 {
		t.Errorf("expected index 1 untouched; got %v", steps[1].Sequential)
	}
}

func TestPatchStep_OutOfRange(t *testing.T) {
	s := newMinimalState()
	s.AppendStep(dsl.ExecutionStep{Sequential: []string{"a"}})

	called := false
	mutate := func(step *dsl.ExecutionStep) { called = true }

	s.PatchStep(-1, mutate)
	if called {
		t.Error("negative index must be a silent no-op")
	}
	s.PatchStep(1, mutate)
	if called {
		t.Error("index == len(steps) must be a silent no-op")
	}
	s.PatchStep(5, mutate)
	if called {
		t.Error("index beyond len(steps) must be a silent no-op")
	}
	if len(s.CollectedSteps()) != 1 || s.CollectedSteps()[0].Sequential[0] != "a" {
		t.Error("out-of-range PatchStep must not mutate existing steps")
	}
}

func TestStepsCount(t *testing.T) {
	s := newMinimalState()
	if s.StepsCount() != 0 {
		t.Fatalf("expected 0; got %d", s.StepsCount())
	}
	s.AppendStep(dsl.ExecutionStep{})
	s.AppendStep(dsl.ExecutionStep{})
	if s.StepsCount() != 2 {
		t.Fatalf("expected 2; got %d", s.StepsCount())
	}
}

func TestSeqBufCount(t *testing.T) {
	s := newMinimalState()
	if s.SeqBufCount() != 0 {
		t.Fatalf("expected 0; got %d", s.SeqBufCount())
	}
	s.AddToSeqBuf("dept-a")
	s.AddToSeqBuf("dept-b")
	s.AddToSeqBuf("dept-a") // duplicate must not grow the buffer
	if s.SeqBufCount() != 2 {
		t.Fatalf("expected 2 after duplicate add; got %d", s.SeqBufCount())
	}
}

func TestNewBranchState(t *testing.T) {
	parent := newMinimalState()
	parent.G.NodeType["shared"] = NodeTypeUserTask
	parent.visited["already"] = true

	bs := NewBranchState(parent, "join1")

	if bs.Proc != parent.Proc {
		t.Error("expected Proc to be shared with parent")
	}
	if bs.G != parent.G {
		t.Error("expected G to be shared with parent")
	}
	if bs.StopAt != "join1" {
		t.Errorf("StopAt = %q; want %q", bs.StopAt, "join1")
	}
	if bs.sd != parent.sd {
		t.Error("expected shared department store between parent and branch")
	}
	if bs.IsVisited("already") {
		t.Error("branch state must have its own independent visited map")
	}
}

func TestRevertBranches(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["gw"] = NodeTypeExclusiveGateway
	s.G.NodeType["back1"] = NodeTypeUserTask
	s.G.Outgoing["back1"] = nil
	s.Proc.UserTasks = []BPMNUserTask{{ID: "back1", Name: "Redo"}}

	branches := s.RevertBranches("gw", []string{"back1"})
	if len(branches) != 1 {
		t.Fatalf("expected 1 branch; got %d", len(branches))
	}
	if branches[0].RevertToNodeID != "back1" {
		t.Errorf("RevertToNodeID = %q; want %q", branches[0].RevertToNodeID, "back1")
	}
}

func TestTraverseBranches_Wrapper(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["split"] = NodeTypeParallelGateway
	s.G.NodeType["a"] = NodeTypeUserTask
	s.G.NodeType["join"] = NodeTypeParallelGateway
	s.G.Outgoing["split"] = []string{"a"}
	s.G.Outgoing["a"] = []string{"join"}
	s.Proc.UserTasks = []BPMNUserTask{{ID: "a"}}

	branches, err := s.TraverseBranches("split", "join")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(branches) != 1 {
		t.Fatalf("expected 1 branch; got %d", len(branches))
	}
}

func TestTraverseExclusiveBranches_Wrapper(t *testing.T) {
	s := newMinimalState()
	s.G.NodeType["split"] = NodeTypeExclusiveGateway
	s.G.NodeType["a"] = NodeTypeEndEvent
	s.G.Outgoing["split"] = []string{"a"}

	branches, err := s.TraverseExclusiveBranches("split", "join")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(branches) != 1 {
		t.Fatalf("expected 1 branch; got %d", len(branches))
	}
}
