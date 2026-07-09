package service

import (
	"context"
	"errors"
	"testing"
)

type errGlueCodec struct{ err error }

func (c errGlueCodec) Encode(_ context.Context, _ string, _ []byte) ([]byte, error) {
	return nil, c.err
}

// buildEnvelope must stamp every outbound event with schema_version "1" so
// consumers can schema-gate (LLD §7.2.1 / §1.8.2).
func TestBuildEnvelope_SetsSchemaVersion(t *testing.T) {
	env, err := buildEnvelope(
		context.Background(),
		noopGlueCodec{},
		"workflow.template.published",
		"11111111-1111-1111-1111-111111111111",
		"", // subject
		"", // actor
		map[string]string{"k": "v"},
	)
	if err != nil {
		t.Fatalf("buildEnvelope: %v", err)
	}
	if env.SchemaVersion != "1" {
		t.Errorf("SchemaVersion = %q, want %q", env.SchemaVersion, "1")
	}
}

func TestBuildEnvelope_GlueEncodeError(t *testing.T) {
	wantErr := errors.New("glue encode boom")
	_, err := buildEnvelope(
		context.Background(),
		errGlueCodec{err: wantErr},
		"workflow.template.published",
		"11111111-1111-1111-1111-111111111111",
		"",
		"",
		map[string]string{"k": "v"},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("buildEnvelope error = %v, want wrapped %v", err, wantErr)
	}
}
