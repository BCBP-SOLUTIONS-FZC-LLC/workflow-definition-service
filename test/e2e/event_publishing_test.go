//go:build integration

package e2e_test

import (
	"context"
	"encoding/json"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/enums"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/events"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

// TestE2E_Promote_EndToEnd verifies that Promote() emits a
// workflow.template.published SNS event with promoted_from_version_id populated.
//
// Publish auto-activates each version, so a "real" Promote event (non-idempotent)
// requires a rollback: Publish V1 (active=V1) → Publish V2 (active=V2) →
// Promote V1 back (active=V1 again, promotedFrom=V2).
func TestE2E_Promote_EndToEnd(t *testing.T) {
	ctx := context.Background()

	pool := fixtures.NewTestPool(t)
	ls := fixtures.NewLocalStackSNSSQS(ctx, t)
	startOutboxRunner(t, ctx, pool, ls)

	svcs := wireServices(t, pool, nil)

	tenantID := uuid.New()
	userID := uuid.New()

	// Publish V1 — Publish auto-activates (active_version_id = V1)
	wf, _, err := svcs.workflow.Create(ctx, tenantID, userID,
		"e2e-promote-test", "E2E Promote Test", "", minimalBPMN, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	draft1, err := svcs.draft.Get(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("Get draft1: %v", err)
	}
	v1, err := svcs.version.Publish(ctx, tenantID, userID, wf.ID, draft1.ID, false)
	if err != nil {
		t.Fatalf("Publish V1: %v", err)
	}

	// Wait for V1 publish event so we can drain it before the next step
	ls.PollQueue(ctx, t, ls.MembershipQueue, 1, 15*time.Second)
	ls.DrainQueue(ctx, t, ls.MembershipQueue)

	// Publish V2 — auto-activates (active_version_id = V2)
	if _, err := svcs.draft.Init(ctx, tenantID, userID, wf.ID); err != nil {
		t.Fatalf("Init draft2: %v", err)
	}
	draft2, err := svcs.draft.Get(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("Get draft2: %v", err)
	}
	v2, err := svcs.version.Publish(ctx, tenantID, userID, wf.ID, draft2.ID, false)
	if err != nil {
		t.Fatalf("Publish V2: %v", err)
	}
	_ = v2

	// Wait for V2 publish event and drain it
	ls.PollQueue(ctx, t, ls.MembershipQueue, 1, 15*time.Second)
	ls.DrainQueue(ctx, t, ls.MembershipQueue)

	// Promote V1 (rollback) — active_version_id was V2, now becomes V1.
	// This is a real Promote: emits workflow.template.published with
	// promoted_from_version_id = V2's ID.
	if _, err := svcs.version.Promote(ctx, tenantID, userID, wf.ID, v1.ID); err != nil {
		t.Fatalf("Promote V1 (rollback): %v", err)
	}

	messages := ls.PollQueue(ctx, t, ls.MembershipQueue, 1, 15*time.Second)
	if len(messages) == 0 {
		t.Fatal("no promote event received on membership-wf-q after 15s")
	}

	var env struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(messages[0]), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v\nbody: %s", err, messages[0])
	}
	if env.Type != string(enums.EventTypeTemplatePublished) {
		t.Errorf("envelope.type = %q, want %q", env.Type, enums.EventTypeTemplatePublished)
	}

	var p events.TemplatePublishedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		t.Fatalf("unmarshal payload: %v\nraw: %s", err, env.Payload)
	}
	if p.VersionID != v1.ID.String() {
		t.Errorf("version_id = %q, want %q", p.VersionID, v1.ID)
	}
	if p.PromotedFromVersionID == nil {
		t.Fatal("promoted_from_version_id must not be nil for a promote (rollback) event")
	}
	if *p.PromotedFromVersionID != v2.ID.String() {
		t.Errorf("promoted_from_version_id = %q, want %q", *p.PromotedFromVersionID, v2.ID)
	}
	if strings.Contains(messages[0], "compiled_plan_json") {
		t.Error("body must not contain compiled_plan_json (SNS 256 KB limit)")
	}
}

// TestE2E_SNSFilterPolicies_QueueRouting verifies that the SNS subscription
// filter on membership-wf-q correctly routes workflow.template.published events.
// An unfiltered queue is created in-test and must receive the same message.
func TestE2E_SNSFilterPolicies_QueueRouting(t *testing.T) {
	ctx := context.Background()

	pool := fixtures.NewTestPool(t)
	ls := fixtures.NewLocalStackSNSSQS(ctx, t)
	startOutboxRunner(t, ctx, pool, ls)

	// Create an unfiltered queue and subscribe it to the topic
	allEventsQURL := ls.CreateQueueAndSubscribe(ctx, t, "e2e-all-events-q", "")

	svcs := wireServices(t, pool, nil)

	tenantID := uuid.New()
	userID := uuid.New()

	wf, _, err := svcs.workflow.Create(ctx, tenantID, userID,
		"e2e-filter-test", "E2E SNS Filter Test", "", minimalBPMN, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	draft, err := svcs.draft.Get(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("Get draft: %v", err)
	}
	if _, err := svcs.version.Publish(ctx, tenantID, userID, wf.ID, draft.ID, false); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	timeout := 15 * time.Second

	// Filtered queue (membership-wf-q) has filter event_type=workflow.template.published
	// — should receive 1 message.
	filtered := ls.PollQueue(ctx, t, ls.MembershipQueue, 1, timeout)
	if len(filtered) != 1 {
		t.Errorf("membership-wf-q: want 1 message, got %d", len(filtered))
	}

	// Unfiltered queue should also receive 1 message.
	unfiltered := ls.PollQueue(ctx, t, allEventsQURL, 1, timeout)
	if len(unfiltered) != 1 {
		t.Errorf("all-events-q: want 1 message, got %d", len(unfiltered))
	}
}
