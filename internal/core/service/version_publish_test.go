package service

import (
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/google/uuid"
)

// TestExtractAssignees_InvalidUUID verifies that extractAssignees skips a
// DefaultAssignees entry that isn't a parseable UUID rather than erroring out
// or emitting a zero-value assignee row.
func TestExtractAssignees_InvalidUUID(t *testing.T) {
	tenantID := uuid.New()
	versionID := uuid.New()
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{
			{
				ID:              "engineering",
				IAMDepartmentID: uuid.New().String(),
				Stages: []dsl.StageDef{
					{
						Type:             "prep",
						Role:             "preparator",
						DefaultAssignees: []string{"not-a-uuid", uuid.New().String()},
					},
				},
			},
		},
	}

	got := extractAssignees(tenantID, versionID, plan)
	if len(got) != 1 {
		t.Fatalf("extractAssignees() returned %d assignees, want 1 (invalid UUID skipped); got %+v", len(got), got)
	}
}

// TestExtractAssignees_NodeKeyComposition pins the node-key form to the one the
// Execution Service uses for task and override keys: <department>/<element id>,
// falling back to the stage type only when a stage carries no element id. Keying
// on the type alone gave two stages of the same type one name between them.
func TestExtractAssignees_NodeKeyComposition(t *testing.T) {
	tenantID, versionID := uuid.New(), uuid.New()
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{
			{
				ID:              "engineering",
				IAMDepartmentID: uuid.New().String(),
				Stages: []dsl.StageDef{
					{Type: "prep", NodeID: "Activity_first", Role: "preparator", DefaultAssignees: []string{uuid.New().String()}},
					{Type: "prep", NodeID: "Activity_second", Role: "preparator", DefaultAssignees: []string{uuid.New().String()}},
					{Type: "gateway", Role: "preparator", DefaultAssignees: []string{uuid.New().String()}},
				},
			},
		},
	}

	got := extractAssignees(tenantID, versionID, plan)
	if len(got) != 3 {
		t.Fatalf("extractAssignees() returned %d assignees, want 3; got %+v", len(got), got)
	}

	keys := make(map[string]struct{}, len(got))
	for _, a := range got {
		keys[a.NodeKey] = struct{}{}
	}
	for _, want := range []string{
		"engineering/Activity_first",
		"engineering/Activity_second",
		"engineering/gateway",
	} {
		if _, ok := keys[want]; !ok {
			t.Errorf("missing node key %q; got %v", want, keys)
		}
	}
	if len(keys) != 3 {
		t.Errorf("two stages of the same type must not collide on one node key; got %v", keys)
	}
}

// A send task, a receive task and a connector stage create no human task, so their free-text
// group and user are not default assignees: the reverse index stores a role of at most 64
// characters, and revoking a membership has no task to pause for them.
func TestExtractAssignees_SkipsStagesThatCreateNoHumanTask(t *testing.T) {
	plan := &dsl.CompiledPlan{
		Departments: []dsl.DepartmentDef{
			{
				ID:              "engineering",
				IAMDepartmentID: uuid.New().String(),
				Stages: []dsl.StageDef{
					{Type: "send_task", NodeID: "Activity_send", Role: strings.Repeat("x", 100), DefaultAssignees: []string{uuid.New().String()}},
					{Type: "receive_task", NodeID: "Activity_receive", Role: "client-rep", DefaultAssignees: []string{uuid.New().String()}},
					{Type: "connector", NodeID: "Activity_call", ConnectorType: "rest-call", Role: "client-rep", DefaultAssignees: []string{uuid.New().String()}},
					{Type: "prep", NodeID: "Activity_prep", Role: "preparator", DefaultAssignees: []string{uuid.New().String()}},
				},
			},
		},
	}

	got := extractAssignees(uuid.New(), uuid.New(), plan)
	if len(got) != 1 || got[0].NodeKey != "engineering/Activity_prep" {
		t.Fatalf("extractAssignees() = %+v; want only the human task's assignee", got)
	}
}
