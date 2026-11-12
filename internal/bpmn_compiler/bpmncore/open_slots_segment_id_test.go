package bpmncore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func openSlotsLane(name string, props map[string]string, refs ...string) BPMNLane {
	lane := BPMNLane{ID: "lane_" + name, Name: name, FlowNodeRefs: refs}
	for k, v := range props {
		lane.ExtensionElements.ZeebeProps.Items = append(lane.ExtensionElements.ZeebeProps.Items, ZeebeProperty{Name: k, Value: v})
	}
	return lane
}

func TestOpenSlots_ProcessWithoutLanes_IsOneImplicitSlotNamedByTheProcessIdBeforeTheAtSign(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   string
		want []string
	}{
		{"plain process id", "Proc_1", []string{"Proc_1"}},
		{"version suffix is cut", "Proc_1@v2", []string{"Proc_1"}},
		{"only the first at sign cuts", "Proc_1@v2@x", []string{"Proc_1"}},
		{"a trailing at sign leaves the id", "Proc_1@", []string{"Proc_1"}},
		{"a leading at sign leaves an empty slot name", "@x", []string{""}},
		{"an empty id is an empty slot name", "", []string{""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := OpenSlots(&BPMNProcess{ID: tc.id})

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestOpenSlots_ProcessWithLanes_ListsTheWorkLanesThatHaveNoDepartmentInLaneOrder(t *testing.T) {
	t.Parallel()

	tasks := []BPMNUserTask{{ID: "T1"}, {ID: "T2"}, {ID: "T3"}, {ID: "T4"}}
	cases := []struct {
		name  string
		lanes []BPMNLane
		want  []string
	}{
		{
			name:  "two open lanes are named in lane order",
			lanes: []BPMNLane{openSlotsLane("Ops", nil, "T1"), openSlotsLane("Eng", nil, "T2")},
			want:  []string{"Ops", "Eng"},
		},
		{
			name: "a lane that names a department is not open",
			lanes: []BPMNLane{
				openSlotsLane("Ops", map[string]string{"dept_id": "dept-1"}, "T1"),
				openSlotsLane("Eng", nil, "T2"),
			},
			want: []string{"Eng"},
		},
		{
			name: "a lane marked ignored is not open",
			lanes: []BPMNLane{
				openSlotsLane("Ops", map[string]string{"ignore": "true"}, "T1"),
				openSlotsLane("Eng", nil, "T2"),
			},
			want: []string{"Eng"},
		},
		{
			name: "a lane without a work task is not open",
			lanes: []BPMNLane{
				openSlotsLane("Ops", nil, "gateway_1"),
				openSlotsLane("Eng", nil, "T2"),
			},
			want: []string{"Eng"},
		},
		{
			name: "a blank dept_id counts as no department",
			lanes: []BPMNLane{
				openSlotsLane("Ops", map[string]string{"dept_id": ""}, "T1"),
			},
			want: []string{"Ops"},
		},
		{
			name: "every lane bound to a department leaves no open slot",
			lanes: []BPMNLane{
				openSlotsLane("Ops", map[string]string{"dept_id": "dept-1"}, "T1"),
				openSlotsLane("Eng", map[string]string{"dept_id": "dept-2"}, "T2"),
			},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc := &BPMNProcess{ID: "Module@v3", LaneSet: BPMNLaneSet{Lanes: tc.lanes}, UserTasks: tasks}

			got := OpenSlots(proc)

			assert.Equal(t, tc.want, got, "the process id is not used once the process has lanes")
		})
	}
}

func TestSegmentID_FirstSegmentIsTheLaneNameAndLaterOnesAppendTheReservedSeparator(t *testing.T) {
	t.Parallel()

	cases := []struct {
		lane string
		n    int
		want string
	}{
		{"Ops", 1, "Ops"},
		{"Ops", 2, "Ops~2"},
		{"Ops", 10, "Ops~10"},
		{"Ops Team", 3, "Ops Team~3"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()

			got := segmentID(tc.lane, tc.n)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSegmentID_ASecondSegmentKeyEqualsWhatALaneNamedWithTheSeparatorWouldBe(t *testing.T) {
	t.Parallel()

	second := segmentID("Ops", 2)

	assert.Equal(t, "Ops~2", second)
	assert.Equal(t, second, segmentID("Ops~2", 1), "the two would share one department key, which is why the validator refuses a lane name with the separator")
}
