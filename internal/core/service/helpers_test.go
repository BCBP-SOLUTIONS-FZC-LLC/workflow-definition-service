package service

import (
	"context"
	"testing"
)

// buildEnvelope must stamp every outbound event with schema_version "1" so
// consumers can schema-gate (LLD §7.2.1 / §1.8.2).
func TestBuildEnvelope_SetsSchemaVersion(t *testing.T) {
	env, err := buildEnvelope(
		context.Background(),
		noopGlueCodec{},
		"workflow.template.published",
		"11111111-1111-1111-1111-111111111111",
		map[string]string{"k": "v"},
	)
	if err != nil {
		t.Fatalf("buildEnvelope: %v", err)
	}
	if env.SchemaVersion != "1" {
		t.Errorf("SchemaVersion = %q, want %q", env.SchemaVersion, "1")
	}
}
