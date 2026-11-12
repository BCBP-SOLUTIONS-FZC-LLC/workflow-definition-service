package element

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
)

func w5dChain(ids ...string) []bpmncore.BPMNSequenceFlow {
	flows := make([]bpmncore.BPMNSequenceFlow, 0, len(ids)-1)
	for i := 0; i+1 < len(ids); i++ {
		flows = append(flows, bpmncore.BPMNSequenceFlow{ID: fmt.Sprintf("f_%s_%s", ids[i], ids[i+1]), SourceRef: ids[i], TargetRef: ids[i+1]})
	}
	return flows
}

func w5dTaskChain(prefix string, n int) ([]bpmncore.BPMNUserTask, []string) {
	tasks := make([]bpmncore.BPMNUserTask, 0, n)
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s%d", prefix, i)
		tasks = append(tasks, bpmncore.BPMNUserTask{ID: id})
		ids = append(ids, id)
	}
	return tasks, ids
}

func w5dCallerOfModuleWithStages(stages int) (*bpmncore.BPMNProcess, *bpmncore.BPMNDefinitions) {
	tasks, ids := w5dTaskChain("m", stages)
	module := bpmncore.BPMNProcess{
		ID:            "Module",
		Name:          "Module",
		StartEvents:   []bpmncore.BPMNEvent{{ID: "mStart"}},
		UserTasks:     tasks,
		EndEvents:     []bpmncore.BPMNEvent{{ID: "mEnd"}},
		SequenceFlows: w5dChain(append(append([]string{"mStart"}, ids...), "mEnd")...),
	}
	root := &bpmncore.BPMNProcess{
		ID:             "Root",
		Name:           "Root",
		StartEvents:    []bpmncore.BPMNEvent{{ID: "start"}},
		EndEvents:      []bpmncore.BPMNEvent{{ID: "end"}},
		CallActivities: []bpmncore.BPMNCallActivity{{ID: "CA", Name: "Call", ExtensionElements: bpmncore.BPMNExtensionElements{ZeebeCalledElement: &bpmncore.ZeebeCalledElement{ProcessID: "Module"}}}},
		SequenceFlows:  w5dChain("start", "CA", "end"),
	}
	return root, &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{*root, module}}
}

func w5dRootWithSubProcessOfStages(before, stages int) *bpmncore.BPMNProcess {
	preTasks, preIDs := w5dTaskChain("pre", before)
	bodyTasks, bodyIDs := w5dTaskChain("b", stages)
	flowIDs := append(append([]string{"start"}, preIDs...), "SP", "end")
	return &bpmncore.BPMNProcess{
		ID:          "Root",
		Name:        "Root",
		StartEvents: []bpmncore.BPMNEvent{{ID: "start"}},
		EndEvents:   []bpmncore.BPMNEvent{{ID: "end"}},
		UserTasks:   preTasks,
		SubProcesses: []bpmncore.BPMNSubProcess{{
			ID:            "SP",
			Name:          "Inner",
			StartEvents:   []bpmncore.BPMNEvent{{ID: "spStart"}},
			EndEvents:     []bpmncore.BPMNEvent{{ID: "spEnd"}},
			UserTasks:     bodyTasks,
			SequenceFlows: w5dChain(append(append([]string{"spStart"}, bodyIDs...), "spEnd")...),
		}},
		SequenceFlows: w5dChain(flowIDs...),
	}
}

func w5dStageCount(plan *dsl.CompiledPlan) int {
	n := 0
	for _, d := range plan.Departments {
		n += len(d.Stages)
	}
	return n
}

func TestCompile_CalledPlanStageCountAgainstTheBudget(t *testing.T) {
	cases := []struct {
		name    string
		stages  int
		wantErr bool
	}{
		{name: "exactly the budget passes", stages: bpmncore.StageBudget},
		{name: "one stage over the budget is refused", stages: bpmncore.StageBudget + 1, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, defs := w5dCallerOfModuleWithStages(tc.stages)

			plan, called, err := bpmncore.Compile(root, bpmncore.BuildGraph(root), map[string]bpmncore.StageTypeHandler{}, DefaultElementHandlers(), defs)

			if tc.wantErr {
				require.ErrorIs(t, err, bpmncore.ErrStageBudget)
				assert.Nil(t, plan)
				assert.Nil(t, called)
				return
			}
			require.NoError(t, err)
			require.Len(t, called, 1)
			assert.Equal(t, tc.stages, w5dStageCount(called[0]))
		})
	}
}

func TestCompile_SubProcessBodyStageCountAgainstTheBudget(t *testing.T) {
	cases := []struct {
		name    string
		before  int
		body    int
		wantErr bool
	}{
		{name: "a body of exactly the budget passes", body: bpmncore.StageBudget},
		{name: "a body one over the budget is refused", body: bpmncore.StageBudget + 1, wantErr: true},
		{name: "a task before the body counts toward the same budget", before: 1, body: bpmncore.StageBudget, wantErr: true},
		{name: "a task before the body and a body that fills the rest passes", before: 1, body: bpmncore.StageBudget - 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := w5dRootWithSubProcessOfStages(tc.before, tc.body)

			plan, called, err := bpmncore.Compile(root, bpmncore.BuildGraph(root), map[string]bpmncore.StageTypeHandler{}, DefaultElementHandlers(), &bpmncore.BPMNDefinitions{Processes: []bpmncore.BPMNProcess{*root}})

			if tc.wantErr {
				require.ErrorIs(t, err, bpmncore.ErrStageBudget)
				assert.Nil(t, plan)
				assert.Nil(t, called)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.before+tc.body, w5dStageCount(plan))
		})
	}
}
