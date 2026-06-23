package bpmncore

import "encoding/xml"

type FlowNodeType string

const (
	NodeTypeStartEvent       FlowNodeType = "startEvent"
	NodeTypeEndEvent         FlowNodeType = "endEvent"
	NodeTypeUserTask         FlowNodeType = "userTask"
	NodeTypeSendTask         FlowNodeType = "sendTask"
	NodeTypeReceiveTask      FlowNodeType = "receiveTask"
	NodeTypeParallelGateway  FlowNodeType = "parallelGateway"
	NodeTypeExclusiveGateway FlowNodeType = "exclusiveGateway"
	NodeTypeInclusiveGateway FlowNodeType = "inclusiveGateway"
	NodeTypeSubProcess       FlowNodeType = "subProcess"
	// NodeTypeBoundaryEvent is registered so sequence flows originating from
	// boundary events pass validateSeqFlowRefs. Boundary events have no incoming
	// sequence flow and are excluded from dangling-node and reachability checks.
	NodeTypeBoundaryEvent FlowNodeType = "boundaryEvent"
)

type BPMNDefinitions struct {
	XMLName       xml.Name           `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL definitions"`
	Processes     []BPMNProcess      `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL process"`
	Collaboration *BPMNCollaboration `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL collaboration"`
	Messages      []BPMNMessage      `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL message"`
	Errors        []BPMNError        `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL error"`
	Diagrams      []BPMNDiagram      `xml:"http://www.omg.org/spec/BPMN/20100524/DI BPMNDiagram"`
}

type BPMNDiagram struct {
	ID    string    `xml:"id,attr"`
	Plane BPMNPlane `xml:"http://www.omg.org/spec/BPMN/20100524/DI BPMNPlane"`
}

type BPMNPlane struct {
	Shapes []BPMNShape `xml:"http://www.omg.org/spec/BPMN/20100524/DI BPMNShape"`
	Edges  []BPMNEdge  `xml:"http://www.omg.org/spec/BPMN/20100524/DI BPMNEdge"`
}

type BPMNShape struct {
	BPMNElement string `xml:"bpmnElement,attr"`
}

type BPMNEdge struct {
	BPMNElement string `xml:"bpmnElement,attr"`
}

type BPMNProcess struct {
	ID                string              `xml:"id,attr"`
	Name              string              `xml:"name,attr"`
	IsExecutable      bool                `xml:"isExecutable,attr"`
	LaneSet           BPMNLaneSet         `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL laneSet"`
	UserTasks         []BPMNUserTask      `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL userTask"`
	SendTasks         []BPMNSendTask      `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL sendTask"`
	ReceiveTasks      []BPMNReceiveTask   `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL receiveTask"`
	StartEvents       []BPMNEvent         `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL startEvent"`
	EndEvents         []BPMNEvent         `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL endEvent"`
	ParallelGateways  []BPMNGateway       `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL parallelGateway"`
	ExclusiveGateways []BPMNGateway       `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL exclusiveGateway"`
	InclusiveGateways []BPMNGateway       `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL inclusiveGateway"`
	SequenceFlows     []BPMNSequenceFlow  `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL sequenceFlow"`
	BoundaryEvents    []BPMNBoundaryEvent `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL boundaryEvent"`
	SubProcesses      []BPMNSubProcess    `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL subProcess"`
}

type BPMNCollaboration struct {
	ID           string            `xml:"id,attr"`
	Participants []BPMNParticipant `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL participant"`
	MessageFlows []BPMNMessageFlow `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL messageFlow"`
}

type BPMNParticipant struct {
	ID         string `xml:"id,attr"`
	Name       string `xml:"name,attr"`
	ProcessRef string `xml:"processRef,attr"`
}

type BPMNMessageFlow struct {
	ID         string `xml:"id,attr"`
	Name       string `xml:"name,attr"`
	SourceRef  string `xml:"sourceRef,attr"`
	TargetRef  string `xml:"targetRef,attr"`
	MessageRef string `xml:"messageRef,attr"`
}

type BPMNMessage struct {
	ID   string `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

type BPMNBoundaryEvent struct {
	ID             string        `xml:"id,attr"`
	AttachedToRef  string        `xml:"attachedToRef,attr"`
	CancelActivity string        `xml:"cancelActivity,attr"`
	Timer          *BPMNTimerDef `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL timerEventDefinition"`
	Error          *BPMNErrorDef `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL errorEventDefinition"`
}

type BPMNTimerDef struct {
	Duration string `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL timeDuration"`
}

type BPMNErrorDef struct {
	ErrorRef string `xml:"errorRef,attr"`
}

type BPMNSubProcess struct {
	ID                string              `xml:"id,attr"`
	Name              string              `xml:"name,attr"`
	LaneSet           BPMNLaneSet         `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL laneSet"`
	UserTasks         []BPMNUserTask      `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL userTask"`
	SendTasks         []BPMNSendTask      `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL sendTask"`
	ReceiveTasks      []BPMNReceiveTask   `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL receiveTask"`
	StartEvents       []BPMNEvent         `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL startEvent"`
	EndEvents         []BPMNEvent         `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL endEvent"`
	ParallelGateways  []BPMNGateway       `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL parallelGateway"`
	ExclusiveGateways []BPMNGateway       `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL exclusiveGateway"`
	InclusiveGateways []BPMNGateway       `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL inclusiveGateway"`
	SequenceFlows     []BPMNSequenceFlow  `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL sequenceFlow"`
	BoundaryEvents    []BPMNBoundaryEvent `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL boundaryEvent"`
}

type BPMNError struct {
	ID        string `xml:"id,attr"`
	ErrorCode string `xml:"errorCode,attr"`
}

type BPMNLaneSet struct {
	Lanes []BPMNLane `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL lane"`
}

type BPMNLane struct {
	ID           string   `xml:"id,attr"`
	Name         string   `xml:"name,attr"`
	FlowNodeRefs []string `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL flowNodeRef"`
}

type BPMNUserTask struct {
	ID                string                `xml:"id,attr"`
	Name              string                `xml:"name,attr"`
	ExtensionElements BPMNExtensionElements `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL extensionElements"`
}

type BPMNSendTask struct {
	ID                string                `xml:"id,attr"`
	Name              string                `xml:"name,attr"`
	ExtensionElements BPMNExtensionElements `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL extensionElements"`
}

type BPMNReceiveTask struct {
	ID                string                `xml:"id,attr"`
	Name              string                `xml:"name,attr"`
	ExtensionElements BPMNExtensionElements `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL extensionElements"`
}

type BPMNEvent struct {
	ID   string `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

type BPMNGateway struct {
	ID   string `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

type BPMNSequenceFlow struct {
	ID                  string                `xml:"id,attr"`
	SourceRef           string                `xml:"sourceRef,attr"`
	TargetRef           string                `xml:"targetRef,attr"`
	ConditionExpression string                `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL conditionExpression"`
	ExtensionElements   BPMNExtensionElements `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL extensionElements"`
}

type BPMNExtensionElements struct {
	TaskDefinition       *ZeebeTaskDefinition       `xml:"http://camunda.org/schema/zeebe/1.0 taskDefinition"`
	AssignmentDefinition *ZeebeAssignmentDefinition `xml:"http://camunda.org/schema/zeebe/1.0 assignmentDefinition"`
	ZeebeProps           BPMNZeebeProperties        `xml:"http://camunda.org/schema/zeebe/1.0 properties"`
}

type ZeebeTaskDefinition struct {
	Type string `xml:"type,attr"`
}

type ZeebeAssignmentDefinition struct {
	CandidateGroups string `xml:"candidateGroups,attr"`
	CandidateUsers  string `xml:"candidateUsers,attr"`
}

type BPMNZeebeProperties struct {
	Items []ZeebeProperty `xml:"http://camunda.org/schema/zeebe/1.0 property"`
}

type ZeebeProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

func PropsMap(ext BPMNExtensionElements) map[string]string {
	m := make(map[string]string, len(ext.ZeebeProps.Items))
	for _, p := range ext.ZeebeProps.Items {
		m[p.Name] = p.Value
	}
	return m
}

func SubProcToProcess(sp *BPMNSubProcess) *BPMNProcess {
	return &BPMNProcess{
		ID:                sp.ID,
		Name:              sp.Name,
		LaneSet:           sp.LaneSet,
		UserTasks:         sp.UserTasks,
		SendTasks:         sp.SendTasks,
		ReceiveTasks:      sp.ReceiveTasks,
		StartEvents:       sp.StartEvents,
		EndEvents:         sp.EndEvents,
		ParallelGateways:  sp.ParallelGateways,
		ExclusiveGateways: sp.ExclusiveGateways,
		InclusiveGateways: sp.InclusiveGateways,
		SequenceFlows:     sp.SequenceFlows,
		BoundaryEvents:    sp.BoundaryEvents,
	}
}
