package bpmncore

import "testing"

func TestPropsMap(t *testing.T) {
	ext := BPMNExtensionElements{ZeebeProps: BPMNZeebeProperties{Items: []ZeebeProperty{
		{Name: "a", Value: "1"},
		{Name: "b", Value: "2"},
	}}}
	m := PropsMap(ext)
	if m["a"] != "1" || m["b"] != "2" {
		t.Errorf("unexpected map: %v", m)
	}
}

func TestPropsMap_Empty(t *testing.T) {
	m := PropsMap(BPMNExtensionElements{})
	if len(m) != 0 {
		t.Errorf("expected empty map; got %v", m)
	}
}

func TestSubProcToProcess(t *testing.T) {
	sp := &BPMNSubProcess{
		ID:                "sp1",
		Name:              "Sub",
		UserTasks:         []BPMNUserTask{{ID: "ut1"}},
		GenericTasks:      []BPMNUserTask{{ID: "gt1"}},
		SendTasks:         []BPMNSendTask{{ID: "st1"}},
		ReceiveTasks:      []BPMNReceiveTask{{ID: "rt1"}},
		StartEvents:       []BPMNEvent{{ID: "s1"}},
		EndEvents:         []BPMNEvent{{ID: "e1"}},
		ParallelGateways:  []BPMNGateway{{ID: "pg1"}},
		ExclusiveGateways: []BPMNGateway{{ID: "eg1"}},
		InclusiveGateways: []BPMNGateway{{ID: "ig1"}},
		SequenceFlows:     []BPMNSequenceFlow{{SourceRef: "s1", TargetRef: "ut1"}},
		BoundaryEvents:    []BPMNBoundaryEvent{{ID: "be1"}},
	}
	proc := SubProcToProcess(sp)
	if proc.ID != "sp1" || proc.Name != "Sub" {
		t.Errorf("unexpected proc: %+v", proc)
	}
	if len(proc.UserTasks) != 2 {
		t.Errorf("expected UserTasks to include GenericTasks; got %d", len(proc.UserTasks))
	}
	if len(proc.SendTasks) != 1 || len(proc.ReceiveTasks) != 1 {
		t.Errorf("unexpected task counts")
	}
	if len(proc.StartEvents) != 1 || len(proc.EndEvents) != 1 {
		t.Errorf("unexpected event counts")
	}
	if len(proc.SequenceFlows) != 1 || len(proc.BoundaryEvents) != 1 {
		t.Errorf("unexpected flow/boundary counts")
	}
}

func TestIsBoundaryEventType(t *testing.T) {
	boundaryTypes := []FlowNodeType{
		NodeTypeBoundaryEvent, NodeTypeTimerBoundaryEvent, NodeTypeErrorBoundaryEvent, NodeTypeMessageBoundaryEvent,
	}
	for _, bt := range boundaryTypes {
		if !IsBoundaryEventType(bt) {
			t.Errorf("expected %v to be a boundary event type", bt)
		}
	}
	if IsBoundaryEventType(NodeTypeUserTask) {
		t.Error("expected userTask to not be a boundary event type")
	}
}
