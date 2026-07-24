//go:build integration

package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/outbox"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

// minimalBPMN is the smallest BPMN that passes the real compiler.
// Shared across all e2e tests that need to publish or compile a version.
const minimalBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"
  xmlns:di="http://www.omg.org/spec/DD/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="E2E Test" isExecutable="true">
    <bpmn:laneSet id="LS1">
      <bpmn:lane id="Lane_eng" name="engineering"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="f14abc9f-befa-54ff-b57f-c15ab3090062"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_prep</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_review</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1" name="Start"/>
    <bpmn:userTask id="Task_prep" name="PrepActivity">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
        <zeebe:properties><zeebe:property name="requires_comment" value="false"/></zeebe:properties>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:userTask id="Task_review" name="ReviewActivity">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="reviewer" candidateUsers="661f9511-f3ac-52e5-b827-557766551111"/>
        <zeebe:properties><zeebe:property name="requires_comment" value="true"/></zeebe:properties>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="End_1" name="End"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start_1"   targetRef="Task_prep"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_prep"  targetRef="Task_review"/>
    <bpmn:sequenceFlow id="F3" sourceRef="Task_review" targetRef="End_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="P1">
      <bpmndi:BPMNShape id="Shape_Start_1"    bpmnElement="Start_1">  <dc:Bounds x="152" y="82" width="36"  height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Shape_Task_prep"  bpmnElement="Task_prep"><dc:Bounds x="240" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Shape_Task_review" bpmnElement="Task_review"><dc:Bounds x="390" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Shape_End_1"      bpmnElement="End_1">    <dc:Bounds x="540" y="82" width="36"  height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_F1" bpmnElement="F1"><di:waypoint x="188" y="100"/><di:waypoint x="240" y="100"/></bpmndi:BPMNEdge>
      <bpmndi:BPMNEdge id="Edge_F2" bpmnElement="F2"><di:waypoint x="340" y="100"/><di:waypoint x="390" y="100"/></bpmndi:BPMNEdge>
      <bpmndi:BPMNEdge id="Edge_F3" bpmnElement="F3"><di:waypoint x="490" y="100"/><di:waypoint x="540" y="100"/></bpmndi:BPMNEdge>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

// services groups the wired service layer for e2e tests.
type services struct {
	workflow *service.WorkflowService
	version  *service.VersionService
	draft    *service.DraftService
}

// wireServices wires WorkflowService, VersionService and DraftService against a
// real pool. Optional cache may be nil. No execution or membership clients.
func wireServices(t *testing.T, pool *pgcommon.Pool, cache port.CacheStore) services {
	t.Helper()

	transactor := pgadapter.NewTransactor(pool)
	workflows := pgadapter.NewWorkflowRepo(pool)
	versions := pgadapter.NewWorkflowVersionRepo(pool)
	assignees := pgadapter.NewAssigneeRepo(pool)
	outboxRepo := pgadapter.NewOutboxRepo(pool)
	compiler := bpmn_compiler.New()

	return services{
		workflow: service.NewWorkflowService(service.WorkflowDeps{
			Transactor: transactor,
			Workflows:  workflows,
			Versions:   versions,
			Cache:      cache,
			Compiler:   compiler,
		}),
		version: service.NewVersionService(service.VersionDeps{
			Transactor: transactor,
			Workflows:  workflows,
			Versions:   versions,
			Assignees:  assignees,
			Outbox:     outboxRepo,
			Compiler:   compiler,
			Cache:      cache,
		}),
		draft: service.NewDraftService(service.DraftDeps{
			Transactor: transactor,
			Workflows:  workflows,
			Versions:   versions,
			Assignees:  assignees,
			Cache:      cache,
			Compiler:   compiler,
		}),
	}
}

// startOutboxRunner starts the outbox relay against the given pool + LocalStack
// and registers cleanup on t. Returns only after the runner goroutine is running.
func startOutboxRunner(t *testing.T, ctx context.Context, pool *pgcommon.Pool, ls *fixtures.LocalStackSNSSQS) {
	t.Helper()

	publisher, err := events.NewSNSPublisher(events.SNSConfig{
		TopicARN:    ls.TopicARN,
		Region:      "us-east-1",
		EndpointURL: ls.EndpointURL,
	})
	if err != nil {
		t.Fatalf("NewSNSPublisher: %v", err)
	}

	runner, err := outbox.NewRunner(outbox.Config{
		Pool:         pool,
		Publisher:    publisher,
		PollInterval: 100 * time.Millisecond,
		BatchSize:    10,
	})
	if err != nil {
		t.Fatalf("outbox.NewRunner: %v", err)
	}

	runnerCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- runner.Start(runnerCtx) }()
	t.Cleanup(func() {
		cancel()
		_ = runner.Stop()
		<-done
	})
}
