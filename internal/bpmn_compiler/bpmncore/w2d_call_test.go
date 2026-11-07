package bpmncore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseStringObject_ObjectSources(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   map[string]string
	}{
		{"leading equals sign and space", `= {"a":"b"}`, map[string]string{"a": "b"}},
		{"equals sign without space", `={"a":"b"}`, map[string]string{"a": "b"}},
		{"surrounding whitespace before the equals sign", "  \t= {\"a\":\"b\"}  ", map[string]string{"a": "b"}},
		{"plain object", `{"a":"b","c":"d"}`, map[string]string{"a": "b", "c": "d"}},
		{"empty object", `{}`, map[string]string{}},
		{"json null", `null`, nil},
		{"json null after the equals sign", `= null`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseStringObject(tc.source)

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			if tc.want == nil {
				assert.Nil(t, got)
			}
		})
	}
}

func TestParseStringObject_NotAnObjectOfStrings_IsRefused(t *testing.T) {
	cases := []struct {
		name   string
		source string
	}{
		{"empty string", ""},
		{"whitespace only", "   "},
		{"equals sign only", "="},
		{"json array", `["a","b"]`},
		{"non-string value", `{"a":1}`},
		{"nested object value", `{"a":{"b":"c"}}`},
		{"truncated json", `{"a":"b"`},
		{"bare string", `"a"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseStringObject(tc.source)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "not a JSON object of strings")
			assert.Nil(t, got)
		})
	}
}

func TestParseCallInputs_NoMappingOrUnknownTargets(t *testing.T) {
	ioCall := func(inputs ...ZeebeIOVar) BPMNCallActivity {
		return BPMNCallActivity{ExtensionElements: BPMNExtensionElements{IOMapping: &ZeebeIOMapping{Inputs: inputs}}}
	}
	cases := []struct {
		name string
		ca   BPMNCallActivity
		want CallInputs
	}{
		{"nil io mapping", BPMNCallActivity{}, CallInputs{}},
		{"io mapping without inputs", ioCall(), CallInputs{}},
		{"unknown targets are ignored", ioCall(
			ZeebeIOVar{Source: `={"a":"b"}`, Target: "depts"},
			ZeebeIOVar{Source: "x", Target: "somethingElse"},
		), CallInputs{}},
		{"dept_id sets the flag only", ioCall(ZeebeIOVar{Source: "x", Target: CallInputDeptID}), CallInputs{HasDeptID: true}},
		{"known and unknown targets together", ioCall(
			ZeebeIOVar{Source: "x", Target: "somethingElse"},
			ZeebeIOVar{Source: `={"Ops":"d1"}`, Target: CallInputDepts},
		), CallInputs{Depts: map[string]string{"Ops": "d1"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ParseCallInputs(tc.ca))
		})
	}
}

func TestParseCallInputs_MalformedSource_RecordsTheErrorOnlyForThatInput(t *testing.T) {
	ca := BPMNCallActivity{ExtensionElements: BPMNExtensionElements{IOMapping: &ZeebeIOMapping{Inputs: []ZeebeIOVar{
		{Source: `[1]`, Target: CallInputDepts},
		{Source: `={"u":"x"}`, Target: CallInputAssignees},
	}}}}

	in := ParseCallInputs(ca)

	require.Error(t, in.DeptsErr)
	assert.Nil(t, in.Depts)
	require.NoError(t, in.AssigneesErr)
	assert.Equal(t, map[string]string{"u": "x"}, in.Assignees)
}

func TestLaneNamed_MatchesCaseInsensitivelyAndFirstLaneWins(t *testing.T) {
	proc := &BPMNProcess{LaneSet: BPMNLaneSet{Lanes: []BPMNLane{
		{ID: "l1", Name: "Ops"},
		{ID: "l2", Name: "ops"},
		{ID: "l3", Name: "Eng"},
	}}}

	cases := []struct {
		name   string
		lookup string
		wantID string
	}{
		{"exact case", "Ops", "l1"},
		{"different case, first of two differing only by case", "OPS", "l1"},
		{"lower case matches the first lane as well", "ops", "l1"},
		{"mixed case other lane", "eNg", "l3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lane := LaneNamed(proc, tc.lookup)

			require.NotNil(t, lane)
			assert.Equal(t, tc.wantID, lane.ID)
		})
	}

	t.Run("unknown name", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, LaneNamed(proc, "Finance"))
	})

	t.Run("name with surrounding spaces is not trimmed", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, LaneNamed(proc, " Ops "))
	})

	t.Run("process without lanes", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, LaneNamed(&BPMNProcess{}, "Ops"))
	})
}

func TestWorkLanes_IncludesOnlyLanesHoldingWorkNodes(t *testing.T) {
	ignore := BPMNExtensionElements{ZeebeProps: BPMNZeebeProperties{Items: []ZeebeProperty{{Name: "ignore", Value: "true"}}}}
	proc := &BPMNProcess{
		UserTasks:         []BPMNUserTask{{ID: "user"}},
		SendTasks:         []BPMNSendTask{{ID: "send"}},
		ReceiveTasks:      []BPMNReceiveTask{{ID: "receive"}},
		CallActivities:    []BPMNCallActivity{{ID: "call"}},
		SubProcesses:      []BPMNSubProcess{{ID: "sub"}},
		StartEvents:       []BPMNEvent{{ID: "start"}},
		EndEvents:         []BPMNEvent{{ID: "end"}},
		ExclusiveGateways: []BPMNGateway{{ID: "gw"}},
		LaneSet: BPMNLaneSet{Lanes: []BPMNLane{
			{Name: "UserLane", FlowNodeRefs: []string{"user"}},
			{Name: "SendLane", FlowNodeRefs: []string{"send"}},
			{Name: "ReceiveLane", FlowNodeRefs: []string{"receive"}},
			{Name: "CallLane", FlowNodeRefs: []string{"call"}},
			{Name: "SubLane", FlowNodeRefs: []string{"sub"}},
			{Name: "ControlOnlyLane", FlowNodeRefs: []string{"start", "gw", "end"}},
			{Name: "EmptyLane"},
			{Name: "IgnoredLane", FlowNodeRefs: []string{"user"}, ExtensionElements: ignore},
			{Name: "MixedLane", FlowNodeRefs: []string{"gw", "send"}},
			{Name: "UnknownRefLane", FlowNodeRefs: []string{"ghost"}},
		}},
	}

	lanes := WorkLanes(proc)

	var names []string
	for _, l := range lanes {
		names = append(names, l.Name)
	}
	assert.Equal(t, []string{"UserLane", "SendLane", "ReceiveLane", "CallLane", "SubLane", "MixedLane"}, names)
}

func TestIgnoreFlag_OnlyTheExactLowerCaseTrueIgnores(t *testing.T) {
	cases := []struct {
		name  string
		items []ZeebeProperty
		want  bool
	}{
		{"true", []ZeebeProperty{{Name: "ignore", Value: "true"}}, true},
		{"capitalised True", []ZeebeProperty{{Name: "ignore", Value: "True"}}, false},
		{"upper case TRUE", []ZeebeProperty{{Name: "ignore", Value: "TRUE"}}, false},
		{"one", []ZeebeProperty{{Name: "ignore", Value: "1"}}, false},
		{"false", []ZeebeProperty{{Name: "ignore", Value: "false"}}, false},
		{"empty value", []ZeebeProperty{{Name: "ignore", Value: ""}}, false},
		{"value with a trailing space", []ZeebeProperty{{Name: "ignore", Value: "true "}}, false},
		{"missing property", nil, false},
		{"other property named differently", []ZeebeProperty{{Name: "Ignore", Value: "true"}}, false},
		{"true among other properties", []ZeebeProperty{{Name: "dept_id", Value: "d"}, {Name: "ignore", Value: "true"}}, true},
	}
	for _, tc := range cases {
		ext := BPMNExtensionElements{ZeebeProps: BPMNZeebeProperties{Items: tc.items}}
		t.Run("process/"+tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, IsIgnored(&BPMNProcess{ExtensionElements: ext}))
		})
		t.Run("lane/"+tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, IsIgnoredLane(&BPMNLane{ExtensionElements: ext}))
		})
	}
}
