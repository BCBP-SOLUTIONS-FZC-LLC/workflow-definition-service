package bpmncore

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func budgetChain(tasks int, withEnd bool) *BPMNProcess {
	proc := &BPMNProcess{StartEvents: []BPMNEvent{{ID: "start"}}}
	prev := "start"
	for i := 0; i < tasks; i++ {
		id := fmt.Sprintf("u%d", i)
		proc.UserTasks = append(proc.UserTasks, BPMNUserTask{ID: id})
		proc.SequenceFlows = append(proc.SequenceFlows, BPMNSequenceFlow{ID: "f_" + id, SourceRef: prev, TargetRef: id})
		prev = id
	}
	if withEnd {
		proc.EndEvents = []BPMNEvent{{ID: "end"}}
		proc.SequenceFlows = append(proc.SequenceFlows, BPMNSequenceFlow{ID: "f_end", SourceRef: prev, TargetRef: "end"})
	}
	return proc
}

func withBoundaryPathOfOneTask(proc *BPMNProcess) *BPMNProcess {
	proc.UserTasks = append(proc.UserTasks, BPMNUserTask{ID: "esc"})
	proc.BoundaryEvents = []BPMNBoundaryEvent{{ID: "be1", AttachedToRef: "u0", Timer: &BPMNTimerDef{}}}
	proc.SequenceFlows = append(proc.SequenceFlows, BPMNSequenceFlow{ID: "f_esc", SourceRef: "be1", TargetRef: "esc"})
	return proc
}

func TestCompile_StageCountAgainstTheBudget(t *testing.T) {
	tests := []struct {
		name    string
		proc    *BPMNProcess
		stages  int
		wantErr bool
	}{
		{name: "exactly the budget with an end event after it passes", proc: budgetChain(StageBudget, true), stages: StageBudget},
		{name: "exactly the budget as the last node passes", proc: budgetChain(StageBudget, false), stages: StageBudget},
		{name: "one stage over the budget as the last node is refused", proc: budgetChain(StageBudget+1, false), wantErr: true},
		{name: "one stage over the budget before an end event is refused", proc: budgetChain(StageBudget+1, true), wantErr: true},
		{name: "a boundary path that takes the plan to budget plus one is refused", proc: withBoundaryPathOfOneTask(budgetChain(StageBudget, true)), wantErr: true},
		{name: "a boundary path that stays within the budget passes", proc: withBoundaryPathOfOneTask(budgetChain(StageBudget-1, true)), stages: StageBudget},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := BuildGraph(tc.proc)
			if tc.proc.BoundaryEvents != nil {
				g.NodeType["esc"] = NodeTypeUserTask
			}
			elements := map[FlowNodeType]ElementHandler{NodeTypeUserTask: &w2cStageChainHandler{}}

			plan, _, err := Compile(tc.proc, g, nil, elements, &BPMNDefinitions{})

			if tc.wantErr {
				require.ErrorIs(t, err, ErrStageBudget)
				assert.Nil(t, plan)
				return
			}
			require.NoError(t, err)
			count := 0
			for _, d := range plan.Departments {
				count += len(d.Stages)
			}
			assert.Equal(t, tc.stages, count)
		})
	}
}
