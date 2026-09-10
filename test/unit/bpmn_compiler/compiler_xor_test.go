package bpmn_compiler_test

import (
	"context"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
)

func TestValidate_XOR_LoopWithAllTerminateBranches(t *testing.T) {
	// XOR_gw has three outgoing:
	//   → End_yes  (forward, terminates)
	//   → End_no   (forward, terminates)
	//   → Task_A   (back-edge from XOR_gw; creates loop: Start→Task_A→XOR_gw→...→Task_A)
	// findJoin returns !ok (no merge node); allBranchesTerminate skips the back-edge
	// and returns true (both forward branches terminate) → valid.
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_XORLoop" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="XOR Loop" isExecutable="true">
    <bpmn:laneSet id="LS">
      <bpmn:lane id="L_ops" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="14649f77-3140-51bf-bdc3-9066944b090f"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_A</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1"/>
    <bpmn:userTask id="Task_A" name="Evaluate">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:exclusiveGateway id="XOR_gw"/>
    <bpmn:endEvent id="End_yes"/>
    <bpmn:endEvent id="End_no"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start_1" targetRef="Task_A"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_A"  targetRef="XOR_gw"/>
    <bpmn:sequenceFlow id="F3" name="yes" sourceRef="XOR_gw"  targetRef="End_yes"/>
    <bpmn:sequenceFlow id="F4" name="no"  sourceRef="XOR_gw"  targetRef="End_no"/>
    <bpmn:sequenceFlow id="F5" name="retry" sourceRef="XOR_gw" targetRef="Task_A"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BD">
    <bpmndi:BPMNPlane id="BP" bpmnElement="P1">
      <bpmndi:BPMNShape id="Sh_Start_1"  bpmnElement="Start_1"><dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_A"   bpmnElement="Task_A"><dc:Bounds x="250" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_XOR_gw"   bpmnElement="XOR_gw"><dc:Bounds x="405" y="75" width="50" height="50"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End_yes"  bpmnElement="End_yes"><dc:Bounds x="510" y="62" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End_no"   bpmnElement="End_no"><dc:Bounds x="510" y="162" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_F1" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Edge_F2" bpmnElement="F2"/>
      <bpmndi:BPMNEdge id="Edge_F3" bpmnElement="F3"/>
      <bpmndi:BPMNEdge id="Edge_F4" bpmnElement="F4"/>
      <bpmndi:BPMNEdge id="Edge_F5" bpmnElement="F5"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(errs) != 0 {
		t.Errorf("expected valid BPMN with guarded loop; got errors: %v", errCodes(errs))
	}
}

// TestHash_SortedZeebeItems_MultipleProps verifies that two tasks with the same
// zeebe:property elements in different orders produce identical hashes — i.e.
// sortedZeebeItems normalises property order before hashing.
func TestHash_SortedZeebeItems_MultipleProps(t *testing.T) {
	makeBPMN := func(propOrder string) string {
		return minimalBPMN(
			`<bpmn:startEvent id="StartEvent_1"/>` +
				`<bpmn:userTask id="Task_1" name="Proposal">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="` + aliceUUID + `"/>
        <zeebe:properties>` + propOrder + `</zeebe:properties>
      </bpmn:extensionElements>
    </bpmn:userTask>` +
				`<bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="F1" sourceRef="StartEvent_1" targetRef="Task_1"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_1"       targetRef="EndEvent_1"/>`,
		)
	}
	propsAB := `<zeebe:property name="aaa" value="1"/><zeebe:property name="zzz" value="2"/>`
	propsBA := `<zeebe:property name="zzz" value="2"/><zeebe:property name="aaa" value="1"/>`

	c := bpmn_compiler.New()
	h1, err := c.Hash(context.Background(), makeBPMN(propsAB))
	if err != nil {
		t.Fatalf("Hash(AB): %v", err)
	}
	h2, err := c.Hash(context.Background(), makeBPMN(propsBA))
	if err != nil {
		t.Fatalf("Hash(BA): %v", err)
	}
	if h1 != h2 {
		t.Errorf("zeebe property order should not affect hash: %q != %q", h1, h2)
	}
}

// TestValidate_XOR_EarlyExitBranchWithJoin_Valid covers the branch in checkSplitJoin
// where an exclusive gateway has a reconverging join but one branch exits early at
// an end event without passing through the join.
func TestValidate_XOR_EarlyExitBranchWithJoin_Valid(t *testing.T) {
	// XOR_split → Task_B → XOR_join → End_main   (normal branch, reconverges)
	// XOR_split → Task_C → XOR_join               (normal branch, reconverges)
	// XOR_split → End_early                        (early exit; valid for exclusive GW)
	bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  id="Def_EarlyExit" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="EarlyExit" isExecutable="true">
    <bpmn:laneSet id="LS1">
      <bpmn:lane id="L_ops" name="ops"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="14649f77-3140-51bf-bdc3-9066944b090f"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_A</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_B</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_C</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1"/>
    <bpmn:userTask id="Task_A" name="Evaluate">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:exclusiveGateway id="XOR_split"/>
    <bpmn:userTask id="Task_B" name="Path B">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:userTask id="Task_C" name="Path C">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="ops" candidateUsers="` + aliceUUID + `"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:exclusiveGateway id="XOR_join"/>
    <bpmn:endEvent id="End_main"/>
    <bpmn:endEvent id="End_early"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start_1"   targetRef="Task_A"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_A"    targetRef="XOR_split"/>
    <bpmn:sequenceFlow id="F3" sourceRef="XOR_split" targetRef="Task_B"/>
    <bpmn:sequenceFlow id="F4" sourceRef="XOR_split" targetRef="Task_C"/>
    <bpmn:sequenceFlow id="F5" sourceRef="XOR_split" targetRef="End_early"/>
    <bpmn:sequenceFlow id="F6" sourceRef="Task_B"    targetRef="XOR_join"/>
    <bpmn:sequenceFlow id="F7" sourceRef="Task_C"    targetRef="XOR_join"/>
    <bpmn:sequenceFlow id="F8" sourceRef="XOR_join"  targetRef="End_main"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="P1">
      <bpmndi:BPMNShape id="Sh_Start_1"   bpmnElement="Start_1"><dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_A"    bpmnElement="Task_A"><dc:Bounds x="250" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_XOR_split" bpmnElement="XOR_split"><dc:Bounds x="405" y="75" width="50" height="50"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_B"    bpmnElement="Task_B"><dc:Bounds x="510" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_Task_C"    bpmnElement="Task_C"><dc:Bounds x="510" y="180" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_XOR_join"  bpmnElement="XOR_join"><dc:Bounds x="665" y="75" width="50" height="50"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End_main"  bpmnElement="End_main"><dc:Bounds x="772" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Sh_End_early" bpmnElement="End_early"><dc:Bounds x="510" y="290" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_F1" bpmnElement="F1"/>
      <bpmndi:BPMNEdge id="Edge_F2" bpmnElement="F2"/>
      <bpmndi:BPMNEdge id="Edge_F3" bpmnElement="F3"/>
      <bpmndi:BPMNEdge id="Edge_F4" bpmnElement="F4"/>
      <bpmndi:BPMNEdge id="Edge_F5" bpmnElement="F5"/>
      <bpmndi:BPMNEdge id="Edge_F6" bpmnElement="F6"/>
      <bpmndi:BPMNEdge id="Edge_F7" bpmnElement="F7"/>
      <bpmndi:BPMNEdge id="Edge_F8" bpmnElement="F8"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmn)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(errs) != 0 {
		t.Errorf("expected valid BPMN with early-exit branch; got errors: %v", errCodes(errs))
	}
}
