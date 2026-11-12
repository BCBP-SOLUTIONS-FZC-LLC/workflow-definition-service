package bpmncore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveMessageFlowTarget_SkipsFlowsThatGiveNoName(t *testing.T) {
	messages := []BPMNMessage{{ID: "Msg_ref", Name: "from-ref"}}
	cases := []struct {
		name  string
		flows []BPMNMessageFlow
		want  string
	}{
		{
			name: "an unnamed first flow does not hide a named later one",
			flows: []BPMNMessageFlow{
				{ID: "MF_1", SourceRef: "A", TargetRef: "BE_1"},
				{ID: "MF_2", Name: "second", SourceRef: "B", TargetRef: "BE_1"},
			},
			want: "second",
		},
		{
			name: "the first named flow wins when several are named",
			flows: []BPMNMessageFlow{
				{ID: "MF_1", Name: "first", SourceRef: "A", TargetRef: "BE_1"},
				{ID: "MF_2", Name: "second", SourceRef: "B", TargetRef: "BE_1"},
			},
			want: "first",
		},
		{
			name: "a flow named through its message ref counts as named",
			flows: []BPMNMessageFlow{
				{ID: "MF_1", SourceRef: "A", TargetRef: "BE_1"},
				{ID: "MF_2", MessageRef: "Msg_ref", SourceRef: "B", TargetRef: "BE_1"},
			},
			want: "from-ref",
		},
		{
			name: "flows into other nodes are not considered",
			flows: []BPMNMessageFlow{
				{ID: "MF_1", Name: "elsewhere", SourceRef: "A", TargetRef: "BE_2"},
				{ID: "MF_2", SourceRef: "B", TargetRef: "BE_1"},
			},
			want: "",
		},
		{
			name: "no flow gives a name",
			flows: []BPMNMessageFlow{
				{ID: "MF_1", SourceRef: "A", TargetRef: "BE_1"},
				{ID: "MF_2", SourceRef: "B", TargetRef: "BE_1"},
			},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defs := &BPMNDefinitions{
				Messages:      messages,
				Collaboration: &BPMNCollaboration{MessageFlows: tc.flows},
			}

			assert.Equal(t, tc.want, ResolveMessageFlowTarget("BE_1", defs))
		})
	}
}
