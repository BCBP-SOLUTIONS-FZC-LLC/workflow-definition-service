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

// The bound is on growth, not wall time, so it holds on a slow runner and
// under the race detector: refusing a process three times larger takes about
// three times as long, where analysing every split would take nine.
func TestValidate_VeryDeepProcess_IsRefusedWithoutAnalysingEverySplit(t *testing.T) {
	validate := func(diamonds int) ([]domain.BPMNValidationError, time.Duration) {
		t.Helper()
		bpmnXML := chainedDiamonds(diamonds)
		start := time.Now()
		errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("Validate of %d diamonds: %v", diamonds, err)
		}
		if !hasCode(errs, domain.BPMNErrMaxDepthExceeded) && !hasCode(errs, domain.BPMNErrTaskLimitExceeded) {
			t.Errorf("%d diamonds: errors = %v, want MAX_DEPTH_EXCEEDED or TASK_LIMIT_EXCEEDED", diamonds, errCodes(errs))
		}
		return errs, elapsed
	}

	if _, err := bpmn_compiler.New().Validate(context.Background(), chainedDiamonds(100)); err != nil {
		t.Fatalf("Validate of the warm-up process: %v", err)
	}
	_, short := validate(800)
	_, long := validate(2400)
	if long > 6*short {
		t.Errorf("a 2400-diamond process took %s, more than 6 times the %s of an 800-diamond process", long, short)
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

// The bound is on growth, not wall time, so it holds on a slow runner and
// under the race detector: refusing a chain four times longer takes about four
// times as long, where a quadratic walk would take sixteen.
func TestValidate_LongChainOfTasklessGateways_IsRefusedQuickly(t *testing.T) {
	validate := func(diamonds int) ([]domain.BPMNValidationError, time.Duration) {
		t.Helper()
		bpmnXML := gatewayOnlyDiamonds(diamonds)
		start := time.Now()
		errs, err := bpmn_compiler.New().Validate(context.Background(), bpmnXML)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("Validate of %d diamonds: %v", diamonds, err)
		}
		return errs, elapsed
	}

	validate(250)
	_, short := validate(1000)
	errs, long := validate(4000)
	if !hasCode(errs, domain.BPMNErrMaxDepthExceeded) {
		t.Errorf("errors = %v, want MAX_DEPTH_EXCEEDED", errCodes(errs))
	}
	if long > 8*short {
		t.Errorf("a 4000-diamond chain took %s, more than 8 times the %s of a 1000-diamond chain", long, short)
	}
	if long > 30*time.Second {
		t.Errorf("Validate of a 4000-diamond gateway chain took %s", long)
	}
}
