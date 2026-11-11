package bpmncore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectorType_ReturnValues(t *testing.T) {
	cases := []struct {
		name     string
		ext      BPMNExtensionElements
		wantName string
		wantOK   bool
	}{
		{"no task definition", BPMNExtensionElements{}, "", false},
		{"empty type", BPMNExtensionElements{TaskDefinition: &ZeebeTaskDefinition{}}, "", false},
		{"type without the connector prefix", BPMNExtensionElements{TaskDefinition: &ZeebeTaskDefinition{Type: "approval"}}, "", false},
		{"type equal to the prefix", BPMNExtensionElements{TaskDefinition: &ZeebeTaskDefinition{Type: ConnectorTaskDefPrefix}}, "", false},
		{"prefixed type", BPMNExtensionElements{TaskDefinition: &ZeebeTaskDefinition{Type: "connector:http_request"}}, "http_request", true},
		{"prefix is case sensitive", BPMNExtensionElements{TaskDefinition: &ZeebeTaskDefinition{Type: "Connector:http_request"}}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			name, ok := ConnectorType(tc.ext)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantName, name)
			}
		})
	}
}

func TestPropsMap_DuplicateNames_LastValueWins(t *testing.T) {
	ext := BPMNExtensionElements{ZeebeProps: BPMNZeebeProperties{Items: []ZeebeProperty{
		{Name: "k", Value: "first"},
		{Name: "other", Value: "x"},
		{Name: "k", Value: "last"},
	}}}

	assert.Equal(t, map[string]string{"k": "last", "other": "x"}, PropsMap(ext))
}

func TestPropsMap_NoItems_ReturnsAnEmptyNonNilMap(t *testing.T) {
	m := PropsMap(BPMNExtensionElements{})

	require.NotNil(t, m)
	assert.Empty(t, m)
	m["writable"] = "yes"
	assert.Equal(t, "yes", m["writable"])
}

func TestBuildMessageStageDef_AssignmentAndExtras(t *testing.T) {
	cases := []struct {
		name          string
		ext           BPMNExtensionElements
		message       string
		wantRole      string
		wantAssignees []string
		wantExtras    map[string]string
	}{
		{
			name:     "candidate groups become the role",
			ext:      BPMNExtensionElements{AssignmentDefinition: &ZeebeAssignmentDefinition{CandidateGroups: "ops"}},
			wantRole: "ops",
		},
		{
			name:          "candidate users are trimmed into one assignee",
			ext:           BPMNExtensionElements{AssignmentDefinition: &ZeebeAssignmentDefinition{CandidateUsers: "  bob  "}},
			wantAssignees: []string{"bob"},
		},
		{
			name: "whitespace-only candidate users give no assignee",
			ext:  BPMNExtensionElements{AssignmentDefinition: &ZeebeAssignmentDefinition{CandidateGroups: "ops", CandidateUsers: " \t "}},

			wantRole: "ops",
		},
		{
			name:          "a comma-separated user list is not split",
			ext:           BPMNExtensionElements{AssignmentDefinition: &ZeebeAssignmentDefinition{CandidateUsers: "alice,bob"}},
			wantAssignees: []string{"alice,bob"},
		},
		{
			name: "properties are copied to the extras",
			ext: BPMNExtensionElements{ZeebeProps: BPMNZeebeProperties{Items: []ZeebeProperty{
				{Name: "a", Value: "1"}, {Name: "b", Value: "2"},
			}}},
			wantExtras: map[string]string{"a": "1", "b": "2"},
		},
		{
			name: "the message name overwrites a property called message",
			ext: BPMNExtensionElements{ZeebeProps: BPMNZeebeProperties{Items: []ZeebeProperty{
				{Name: "message", Value: "from-property"}, {Name: "a", Value: "1"},
			}}},
			message:    "OrderPlaced",
			wantExtras: map[string]string{"message": "OrderPlaced", "a": "1"},
		},
		{
			name:       "a property called message survives when there is no message name",
			ext:        BPMNExtensionElements{ZeebeProps: BPMNZeebeProperties{Items: []ZeebeProperty{{Name: "message", Value: "from-property"}}}},
			wantExtras: map[string]string{"message": "from-property"},
		},
		{
			name:       "duplicate property names keep the last value",
			ext:        BPMNExtensionElements{ZeebeProps: BPMNZeebeProperties{Items: []ZeebeProperty{{Name: "k", Value: "1"}, {Name: "k", Value: "2"}}}},
			wantExtras: map[string]string{"k": "2"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stage := BuildMessageStageDef("n1", "Name", "send_task", tc.message, tc.ext)

			assert.Equal(t, tc.wantRole, stage.Role)
			assert.Equal(t, tc.wantAssignees, stage.DefaultAssignees)
			assert.Equal(t, tc.wantExtras, stage.Extras)
		})
	}
}

func TestSubProcToProcess_NestedContentAndExtensions_AreNotCarriedOver(t *testing.T) {
	sp := &BPMNSubProcess{
		ID:   "sp1",
		Name: "Outer",
		ExtensionElements: BPMNExtensionElements{
			TaskDefinition: &ZeebeTaskDefinition{Type: "x"},
			ZeebeProps:     BPMNZeebeProperties{Items: []ZeebeProperty{{Name: "ignore", Value: "true"}}},
		},
		SubProcesses:   []BPMNSubProcess{{ID: "inner", UserTasks: []BPMNUserTask{{ID: "innerTask"}}}},
		ServiceTasks:   []BPMNUserTask{{ID: "svc"}},
		UserTasks:      []BPMNUserTask{{ID: "ut"}},
		GenericTasks:   []BPMNUserTask{{ID: "gen"}},
		CallActivities: []BPMNCallActivity{{ID: "call"}},
		LaneSet:        BPMNLaneSet{Lanes: []BPMNLane{{Name: "Ops", FlowNodeRefs: []string{"ut"}}}},
	}

	proc := SubProcToProcess(sp)

	assert.Empty(t, proc.SubProcesses)
	assert.Empty(t, proc.ServiceTasks)
	assert.Equal(t, BPMNExtensionElements{}, proc.ExtensionElements)
	assert.Equal(t, []BPMNUserTask{{ID: "ut"}, {ID: "gen"}}, proc.UserTasks, "generic tasks join the user tasks; the nested sub-process task and the service task do not")
	require.Len(t, proc.CallActivities, 1)
	assert.Equal(t, "call", proc.CallActivities[0].ID)
	require.Len(t, proc.LaneSet.Lanes, 1)
	assert.Equal(t, "Ops", proc.LaneSet.Lanes[0].Name)
}
