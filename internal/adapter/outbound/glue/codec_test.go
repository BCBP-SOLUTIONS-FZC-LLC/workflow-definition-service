package glue

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/google/uuid"
)

func TestCodec_Encode_StubMode(t *testing.T) {
	codec := NewCodec(aws.Config{}, "workflow-template-events", true, "")
	payload := []byte(`{"workflow_id":"123"}`)

	encoded, err := codec.Encode(context.Background(), "WorkflowTemplatePublished", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Equal(encoded, payload) {
		t.Errorf("stub mode should return raw payload, got: %s", string(encoded))
	}
}

func TestCodec_Encode_WithCachedSchema(t *testing.T) {
	codec := NewCodec(aws.Config{}, "workflow-template-events", false, "")
	payload := []byte(`{"workflow_id":"123"}`)

	mockUUID := uuid.New()
	binaryBytes, err := mockUUID.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal uuid failed: %v", err)
	}

	schemaName := "WorkflowTemplatePublished"
	codec.cache[schemaName] = cacheEntry{
		versionIDBytes: binaryBytes,
		expiresAt:      time.Now().Add(5 * time.Minute),
	}

	encoded, err := codec.Encode(context.Background(), schemaName, payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedLen := 2 + len(binaryBytes) + len(payload)
	if len(encoded) != expectedLen {
		t.Fatalf("expected length %d, got %d", expectedLen, len(encoded))
	}

	if encoded[0] != 3 {
		t.Errorf("expected magic byte 3, got %d", encoded[0])
	}
	if encoded[1] != 0 {
		t.Errorf("expected compression type 0, got %d", encoded[1])
	}

	extractedUUIDBytes := encoded[2 : 2+len(binaryBytes)]
	if !bytes.Equal(extractedUUIDBytes, binaryBytes) {
		t.Errorf("extracted UUID bytes do not match expected binary UUID")
	}

	extractedPayload := encoded[2+len(binaryBytes):]
	if !bytes.Equal(extractedPayload, payload) {
		t.Errorf("extracted payload %s does not match expected %s", string(extractedPayload), string(payload))
	}
}
