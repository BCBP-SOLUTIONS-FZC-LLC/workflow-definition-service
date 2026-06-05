package bpmn_compiler

import (
	"encoding/xml"
	"strings"
)

const (
	nsBPMN  = "http://www.omg.org/spec/BPMN/20100524/MODEL"
	nsZeebe = "http://camunda.org/schema/zeebe/1.0"
)

type FlowNodeType string

const (
	NodeTypeStartEvent       FlowNodeType = "startEvent"
	NodeTypeEndEvent         FlowNodeType = "endEvent"
	NodeTypeUserTask         FlowNodeType = "userTask"
	NodeTypeParallelGateway  FlowNodeType = "parallelGateway"
	NodeTypeExclusiveGateway FlowNodeType = "exclusiveGateway"
)

// Processes is a slice (not a single field) so the compiler can surface a
// MULTIPLE_PROCESSES error rather than silently discarding extra process elements.
type bpmnDefinitions struct {
	XMLName   xml.Name      `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL definitions"`
	Processes []bpmnProcess `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL process"`
}

type bpmnProcess struct {
	ID                string             `xml:"id,attr"`
	Name              string             `xml:"name,attr"`
	LaneSet           bpmnLaneSet        `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL laneSet"`
	UserTasks         []bpmnUserTask     `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL userTask"`
	StartEvents       []bpmnEvent        `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL startEvent"`
	EndEvents         []bpmnEvent        `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL endEvent"`
	ParallelGateways  []bpmnGateway      `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL parallelGateway"`
	ExclusiveGateways []bpmnGateway      `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL exclusiveGateway"`
	SequenceFlows     []bpmnSequenceFlow `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL sequenceFlow"`
}

type bpmnLaneSet struct {
	Lanes []bpmnLane `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL lane"`
}

type bpmnLane struct {
	ID           string   `xml:"id,attr"`
	Name         string   `xml:"name,attr"`
	FlowNodeRefs []string `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL flowNodeRef"`
}

type bpmnUserTask struct {
	ID                string                `xml:"id,attr"`
	Name              string                `xml:"name,attr"`
	ExtensionElements bpmnExtensionElements `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL extensionElements"`
}

type bpmnEvent struct {
	ID   string `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

type bpmnGateway struct {
	ID   string `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

type bpmnSequenceFlow struct {
	ID                string                `xml:"id,attr"`
	SourceRef         string                `xml:"sourceRef,attr"`
	TargetRef         string                `xml:"targetRef,attr"`
	ExtensionElements bpmnExtensionElements `xml:"http://www.omg.org/spec/BPMN/20100524/MODEL extensionElements"`
}

// Intermediate struct required because Go's xml path tag does not resolve
// cross-namespace path components (a>b where a and b have different namespace URIs).
type bpmnExtensionElements struct {
	ZeebeProps bpmnZeebeProperties `xml:"http://camunda.org/schema/zeebe/1.0 properties"`
}

type bpmnZeebeProperties struct {
	Items []zeebeProperty `xml:"http://camunda.org/schema/zeebe/1.0 property"`
}

type zeebeProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

func propsMap(ext bpmnExtensionElements) map[string]string {
	m := make(map[string]string, len(ext.ZeebeProps.Items))
	for _, p := range ext.ZeebeProps.Items {
		m[p.Name] = p.Value
	}
	return m
}

func normalizeLaneName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
