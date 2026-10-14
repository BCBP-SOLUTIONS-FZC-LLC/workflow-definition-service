package bpmn_compiler_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func chainedDiamonds(count int) string {
	var body strings.Builder
	nodes := []string{"S", "E"}
	body.WriteString("    <bpmn:startEvent id=\"S\"/>\n    <bpmn:endEvent id=\"E\"/>\n")
	prev := "S"
	flow := 0
	link := func(from, to string) {
		flow++
		body.WriteString(flowXML(fmt.Sprintf("F%d", flow), from, to))
	}
	for i := 0; i < count; i++ {
		split, a, b, join := fmt.Sprintf("P%d", i), fmt.Sprintf("A%d", i), fmt.Sprintf("B%d", i), fmt.Sprintf("J%d", i)
		fmt.Fprintf(&body, "    <bpmn:parallelGateway id=%q/>\n    <bpmn:parallelGateway id=%q/>\n", split, join)
		body.WriteString(userTaskXML(a) + userTaskXML(b))
		nodes = append(nodes, split, join, a, b)
		link(prev, split)
		link(split, a)
		link(split, b)
		link(a, join)
		link(b, join)
		prev = join
	}
	link(prev, "E")
	return flowDocument([]flowLane{{"Engineering", engDeptID, nodes}}, body.String())
}

func TestValidate_VeryDeepProcess_IsRefusedWithoutAnalysingEverySplit(t *testing.T) {
	bpmnXML := chainedDiamonds(1200)

	start := time.Now()
	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrMaxDepthExceeded) && !hasCode(errs, domain.BPMNErrTaskLimitExceeded) {
		t.Errorf("errors = %v, want MAX_DEPTH_EXCEEDED or TASK_LIMIT_EXCEEDED", errCodes(errs))
	}
	if elapsed > 3*time.Second {
		t.Errorf("Validate of a process the limits refuse took %s", elapsed)
	}
}

func gatewayOnlyDiamonds(count int) string {
	var body strings.Builder
	nodes := []string{"S", "E"}
	body.WriteString("    <bpmn:startEvent id=\"S\"/>\n    <bpmn:endEvent id=\"E\"/>\n")
	prev := "S"
	flow := 0
	link := func(from, to string) {
		flow++
		body.WriteString(flowXML(fmt.Sprintf("F%d", flow), from, to))
	}
	for i := 0; i < count; i++ {
		split, join := fmt.Sprintf("P%d", i), fmt.Sprintf("J%d", i)
		fmt.Fprintf(&body, "    <bpmn:parallelGateway id=%q/>\n    <bpmn:parallelGateway id=%q/>\n", split, join)
		nodes = append(nodes, split, join)
		link(prev, split)
		link(split, join)
		link(split, join)
		prev = join
	}
	link(prev, "E")
	return flowDocument([]flowLane{{"Engineering", engDeptID, nodes}}, body.String())
}

func TestValidate_LongChainOfTasklessGateways_IsRefusedQuickly(t *testing.T) {
	bpmnXML := gatewayOnlyDiamonds(4000)

	start := time.Now()
	errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !hasCode(errs, domain.BPMNErrMaxDepthExceeded) {
		t.Errorf("errors = %v, want MAX_DEPTH_EXCEEDED", errCodes(errs))
	}
	if elapsed > 3*time.Second {
		t.Errorf("Validate of a 4000-diamond gateway chain took %s", elapsed)
	}
}
