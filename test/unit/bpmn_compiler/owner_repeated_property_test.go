package bpmn_compiler_test

import (
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func zeebeProperties(pairs ...string) string {
	var b strings.Builder
	b.WriteString("<bpmn:extensionElements><zeebe:properties>")
	for i := 0; i+1 < len(pairs); i += 2 {
		b.WriteString(`<zeebe:property name="` + pairs[i] + `" value="` + pairs[i+1] + `"/>`)
	}
	b.WriteString("</zeebe:properties></bpmn:extensionElements>")
	return b.String()
}

func straightBody(taskElement, flowElement string) string {
	return "    <bpmn:startEvent id=\"S\"/>\n" + taskElement + "    <bpmn:endEvent id=\"E\"/>\n" + flowXML("F1", "S", "A") + flowElement
}

func taskWithProperties(id, properties string) string {
	return `    <bpmn:userTask id="` + id + `"><bpmn:extensionElements><zeebe:taskDefinition type="prep"/><zeebe:assignmentDefinition candidateGroups="preparator" candidateUsers="` + validCandidateUser + `"/>` +
		strings.TrimSuffix(strings.TrimPrefix(properties, "<bpmn:extensionElements>"), "</bpmn:extensionElements>") + `</bpmn:extensionElements></bpmn:userTask>` + "\n"
}

func TestValidate_PropertyWrittenTwiceOnOneElement_IsRefusedOnThatElement(t *testing.T) {
	laneProp := `<zeebe:property name="dept_id" value="` + engDeptID + `"/>`
	cases := map[string]struct {
		doc  string
		node string
	}{
		"a lane's dept_id, two values": {
			doc:  strings.Replace(flowDocument(oneLane("S", "A", "E"), straightBody(userTaskXML("A"), flowXML("F2", "A", "E"))), laneProp, laneProp+`<zeebe:property name="dept_id" value="`+opsDeptID+`"/>`, 1),
			node: "Lane_0",
		},
		"a lane's dept_id, the same value": {
			doc:  strings.Replace(flowDocument(oneLane("S", "A", "E"), straightBody(userTaskXML("A"), flowXML("F2", "A", "E"))), laneProp, laneProp+laneProp, 1),
			node: "Lane_0",
		},
		"a task's property": {
			doc:  flowDocument(oneLane("S", "A", "E"), straightBody(taskWithProperties("A", zeebeProperties("region", "eu", "region", "us")), flowXML("F2", "A", "E"))),
			node: "A",
		},
		"a sequence flow's property": {
			doc: flowDocument(oneLane("S", "A", "E"), straightBody(userTaskXML("A"),
				`    <bpmn:sequenceFlow id="F2" sourceRef="A" targetRef="E">`+zeebeProperties("k", "1", "k", "2")+`</bpmn:sequenceFlow>`+"\n")),
			node: "F2",
		},
		"the process ignore flag": {
			doc: flowDocument(oneLane("S", "A", "E"), straightBody(userTaskXML("A"), flowXML("F2", "A", "E")),
				`  <bpmn:process id="Outside" isExecutable="false">`+zeebeProperties("ignore", "false", "ignore", "true")+`</bpmn:process>`),
			node: "Outside",
		},
		"a call activity's property": {
			doc: flowDocument(oneLane("S", "C", "E"),
				"    <bpmn:startEvent id=\"S\"/>\n    <bpmn:callActivity id=\"C\"><bpmn:extensionElements><zeebe:calledElement processId=\"Mod\"/>"+
					strings.TrimSuffix(strings.TrimPrefix(zeebeProperties("k", "1", "k", "2"), "<bpmn:extensionElements>"), "</bpmn:extensionElements>")+
					"</bpmn:extensionElements></bpmn:callActivity>\n    <bpmn:endEvent id=\"E\"/>\n"+flowXML("F1", "S", "C")+flowXML("F2", "C", "E"),
				lanedModule("Mod", moduleLane{"Rev", "", "M_T"})),
			node: "C",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assertErrorOn(t, validateDocument(t, tc.doc), domain.BPMNErrInvalidZeebeProperty, tc.node)
			assertErrorOn(t, validateForPublishDocument(t, tc.doc), domain.BPMNErrInvalidZeebeProperty, tc.node)
		})
	}
}

func TestValidate_PropertiesWithDistinctNamesOrOnDifferentElements_AreValid(t *testing.T) {
	doc := flowDocument([]flowLane{{"Engineering", engDeptID, []string{"S", "A", "E"}}, {"Ops", opsDeptID, nil}},
		straightBody(taskWithProperties("A", zeebeProperties("region", "eu", "tier", "1")), flowXML("F2", "A", "E")))

	assertNoError(t, validateDocument(t, doc), domain.BPMNErrInvalidZeebeProperty)
}
