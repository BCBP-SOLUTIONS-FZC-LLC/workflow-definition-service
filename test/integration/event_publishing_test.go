//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/outbox"
	"github.com/google/uuid"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

// minimalBPMN is the smallest BPMN that passes the real compiler:
// one lane with a start event, three user tasks (prep/review/approve), an end event,
// and a BPMNDiagram with shapes and edges for every element (required by diagram validator).
const minimalBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"
  xmlns:di="http://www.omg.org/spec/DD/20100524/DI"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="Event Publishing Test" isExecutable="true">
    <bpmn:laneSet id="LS1">
      <bpmn:lane id="Lane_eng" name="engineering">
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_prep</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_review</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>Task_approve</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1" name="Start"/>
    <bpmn:userTask id="Task_prep" name="PrepActivity">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="prep"/>
        <zeebe:assignmentDefinition candidateGroups="preparer" candidateUsers="550e8400-e29b-41d4-a716-446655440000"/>
        <zeebe:properties>
          <zeebe:property name="requires_comment" value="false"/>
        </zeebe:properties>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:userTask id="Task_review" name="ReviewActivity">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="reviewer" candidateUsers="661f9511-f3ac-52e5-b827-557766551111"/>
        <zeebe:properties>
          <zeebe:property name="requires_comment" value="true"/>
        </zeebe:properties>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:userTask id="Task_approve" name="ApproveActivity">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="approve"/>
        <zeebe:assignmentDefinition candidateGroups="approver" candidateUsers="883b1733-15ce-74a7-da49-779988773333"/>
        <zeebe:properties>
          <zeebe:property name="requires_comment" value="true"/>
        </zeebe:properties>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="End_1" name="End"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start_1"    targetRef="Task_prep"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Task_prep"   targetRef="Task_review"/>
    <bpmn:sequenceFlow id="F3" sourceRef="Task_review" targetRef="Task_approve"/>
    <bpmn:sequenceFlow id="F4" sourceRef="Task_approve" targetRef="End_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="P1">
      <bpmndi:BPMNShape id="Shape_Start_1"    bpmnElement="Start_1">   <dc:Bounds x="152" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Shape_Task_prep"  bpmnElement="Task_prep"> <dc:Bounds x="240" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Shape_Task_review" bpmnElement="Task_review"><dc:Bounds x="390" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Shape_Task_approve" bpmnElement="Task_approve"><dc:Bounds x="540" y="60" width="100" height="80"/></bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Shape_End_1"      bpmnElement="End_1">     <dc:Bounds x="692" y="82" width="36" height="36"/></bpmndi:BPMNShape>
      <bpmndi:BPMNEdge id="Edge_F1" bpmnElement="F1"><di:waypoint x="188" y="100"/><di:waypoint x="240" y="100"/></bpmndi:BPMNEdge>
      <bpmndi:BPMNEdge id="Edge_F2" bpmnElement="F2"><di:waypoint x="340" y="100"/><di:waypoint x="390" y="100"/></bpmndi:BPMNEdge>
      <bpmndi:BPMNEdge id="Edge_F3" bpmnElement="F3"><di:waypoint x="490" y="100"/><di:waypoint x="540" y="100"/></bpmndi:BPMNEdge>
      <bpmndi:BPMNEdge id="Edge_F4" bpmnElement="F4"><di:waypoint x="640" y="100"/><di:waypoint x="692" y="100"/></bpmndi:BPMNEdge>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

// TestEventPublishing_PublishVersion_SNSPayload is an end-to-end test that:
//  1. Starts a real Postgres + LocalStack container (SNS, SQS, Glue).
//  2. Wires a real VersionService with real repos and a real SNS publisher pointed at LocalStack.
//  3. Publishes a workflow version.
//  4. Runs the outbox relay until the message lands on the SQS queue.
//  5. Verifies the SQS message body matches the expected TemplatePublishedPayload.
//  6. Verifies the Glue schema registry was populated correctly.
func TestEventPublishing_PublishVersion_SNSPayload(t *testing.T) {
	ctx := context.Background()

	pool := fixtures.NewTestPool(t)
	ls := fixtures.NewLocalStackSNSSQS(ctx, t)

	if id := ls.SchemaVersionID(ctx, t); id == "" && ls.GlueAvailable {
		t.Error("Glue schema version ID must be non-empty when Glue is available")
	}

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

	workflowRepo := pgadapter.NewWorkflowRepo(pool)
	versionRepo := pgadapter.NewWorkflowVersionRepo(pool)
	svc := service.NewVersionService(service.VersionDeps{
		Transactor: pgadapter.NewTransactor(pool),
		Workflows:  workflowRepo,
		Versions:   versionRepo,
		Assignees:  pgadapter.NewAssigneeRepo(pool),
		Outbox:     pgadapter.NewOutboxRepo(pool),
		Compiler:   bpmn_compiler.New(),
	})

	tenantID, userID, wfID, versionID := seedPublishFixtures(t, ctx, workflowRepo, versionRepo)

	runnerCtx, cancelRunner := context.WithCancel(ctx)
	runnerDone := make(chan error, 1)
	go func() { runnerDone <- runner.Start(runnerCtx) }()
	t.Cleanup(func() {
		cancelRunner()
		_ = runner.Stop()
		<-runnerDone
	})

	if _, err = svc.Publish(ctx, tenantID, userID, wfID, versionID, false); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	messages := ls.PollQueue(ctx, t, ls.MembershipQueue, 1, 15*time.Second)
	if len(messages) == 0 {
		t.Fatal("no messages received on membership-wf-q after 15 s")
	}
	assertSNSPayload(t, messages[0], wfID, versionID, userID)
}

// seedPublishFixtures creates a workflow and a draft version in Postgres, returning
// the tenantID, userID, workflowID, and versionID for use in the test.
func seedPublishFixtures(
	t *testing.T,
	ctx context.Context,
	workflowRepo interface{ Create(context.Context, *domain.Workflow) error },
	versionRepo interface{ Create(context.Context, *domain.WorkflowVersion) error },
) (tenantID, userID, wfID, versionID uuid.UUID) {
	t.Helper()

	tenantID = uuid.New()
	userID = uuid.New()

	wfID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid wfID: %v", err)
	}
	versionID, err = uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid versionID: %v", err)
	}

	wf := &domain.Workflow{
		ID: wfID, TenantID: tenantID, CreatedByUserID: userID,
		BusinessKey: "event-publish-integ-test",
		Name:        "Event Publishing Integration Test",
	}
	if err := workflowRepo.Create(ctx, wf); err != nil {
		t.Fatalf("Create workflow: %v", err)
	}

	draft := &domain.WorkflowVersion{
		ID: versionID, WorkflowID: wfID, TenantID: tenantID,
		CreatedByUserID: userID,
		Status:          domain.VersionStatusDraft,
		BPMNXML:         minimalBPMN,
		IsValid:         true,
	}
	if err := versionRepo.Create(ctx, draft); err != nil {
		t.Fatalf("Create version: %v", err)
	}

	return tenantID, userID, wfID, versionID
}

// assertSNSPayload decodes the raw SQS message body and verifies that:
//   - the envelope type matches EventTypeTemplatePublished
//   - the payload fields match the expected IDs and business key
//   - compiled_plan_json is absent (LLD §10.7 — SNS 256 KB hard limit)
func assertSNSPayload(t *testing.T, body string, wfID, versionID, userID uuid.UUID) {
	t.Helper()

	// With RawMessageDelivery=true and noopGlueCodec, the SQS body is the raw
	// serialised events.Envelope[json.RawMessage].
	var env struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v\nbody: %s", err, body)
	}
	if env.Type != string(domain.EventTypeTemplatePublished) {
		t.Errorf("envelope.type = %q, want %q", env.Type, domain.EventTypeTemplatePublished)
	}

	var p domain.TemplatePublishedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		t.Fatalf("unmarshal payload: %v\nraw: %s", err, env.Payload)
	}
	if p.WorkflowID != wfID.String() {
		t.Errorf("workflow_id = %q, want %q", p.WorkflowID, wfID.String())
	}
	if p.WorkflowKey != "event-publish-integ-test" {
		t.Errorf("workflow_key = %q, want %q", p.WorkflowKey, "event-publish-integ-test")
	}
	if p.VersionID != versionID.String() {
		t.Errorf("version_id = %q, want %q", p.VersionID, versionID.String())
	}
	if p.VersionNumber != 1 {
		t.Errorf("version_number = %d, want 1", p.VersionNumber)
	}
	if p.ArtifactHash == "" {
		t.Error("artifact_hash must not be empty")
	}
	if p.PublishedBy != userID.String() {
		t.Errorf("published_by = %q, want %q", p.PublishedBy, userID.String())
	}
	if p.PromotedFromVersionID != nil {
		t.Errorf("promoted_from_version_id = %v, want nil", *p.PromotedFromVersionID)
	}
	if strings.Contains(body, "compiled_plan_json") {
		t.Errorf("body must not contain compiled_plan_json (SNS 256 KB limit): %s", body)
	}
}
