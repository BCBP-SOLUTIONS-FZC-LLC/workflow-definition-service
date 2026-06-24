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

// minimalBPMN is a structurally valid single-process BPMN the real compiler accepts.
const minimalBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  id="D1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="P1" name="Event Publishing Test">
    <bpmn:startEvent id="Start_1" name="Start"/>
    <bpmn:endEvent   id="End_1"   name="End"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start_1" targetRef="End_1"/>
  </bpmn:process>
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

	if ls.SchemaVersionID(ctx, t) == "" {
		t.Error("Glue schema version ID must be non-empty after registry creation")
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
