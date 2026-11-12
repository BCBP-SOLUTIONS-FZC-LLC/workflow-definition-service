package bpmn_compiler_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
)

func tqSendTaskBPMN(messages, sendAttrs string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
%[1]s
  <bpmn:process id="Proc_1" name="Test" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_design" name="design"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="cdae8dac-0c71-5ae9-8ecc-74a1cdc0bda3"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>StartEvent_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_send</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>EndEvent_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="StartEvent_1"/>
    <bpmn:sendTask id="Task_send" name="Notify"%[2]s/>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_send"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_send" targetRef="EndEvent_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Proc_1">
      <bpmndi:BPMNShape id="Sh_Start" bpmnElement="StartEvent_1"><dc:Bounds x="0" y="0" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Send" bpmnElement="Task_send"><dc:Bounds x="100" y="0" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End" bpmnElement="EndEvent_1"><dc:Bounds x="250" y="0" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_F1" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Edge_F2" bpmnElement="F2"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`, messages, sendAttrs)
}

func TestCompile_SendTaskMessageRef_MessageExtraOnlyWhenResolvable(t *testing.T) {
	cases := []struct {
		name        string
		messages    string
		sendAttrs   string
		wantMessage string
	}{
		{name: "no messageRef", messages: "", sendAttrs: ""},
		{name: "messageRef to a message that is not defined", messages: "", sendAttrs: ` messageRef="Msg_missing"`},
		{name: "messageRef to another message than the one defined", messages: `<bpmn:message id="Msg_other" name="other.name"/>`, sendAttrs: ` messageRef="Msg_missing"`},
		{name: "resolvable messageRef carries the message name", messages: `<bpmn:message id="Msg_notify" name="order.notify"/>`, sendAttrs: ` messageRef="Msg_notify"`, wantMessage: "order.notify"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bpmn := tqSendTaskBPMN(tc.messages, tc.sendAttrs)
			c := bpmn_compiler.New()

			issues, err := c.Validate(context.Background(), bpmn)
			require.NoError(t, err)
			assert.Empty(t, issues, "an unresolvable messageRef on a send task is not a validation issue")

			plan, err := c.Compile(context.Background(), bpmn)
			require.NoError(t, err)
			require.Len(t, plan.Departments, 1)
			require.Len(t, plan.Departments[0].Stages, 1)
			stage := plan.Departments[0].Stages[0]
			assert.Equal(t, "send_task", stage.Type)
			assert.Equal(t, "Task_send", stage.NodeID)
			msg, hasMessage := stage.Extras["message"]
			if tc.wantMessage == "" {
				assert.False(t, hasMessage, "no message extra expected, got %q", msg)
				return
			}
			assert.Equal(t, tc.wantMessage, msg)
		})
	}
}
