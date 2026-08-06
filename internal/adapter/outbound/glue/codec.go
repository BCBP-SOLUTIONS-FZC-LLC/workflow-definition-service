package glue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	"github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"
)

var _ events.Codec = (*Codec)(nil)

// Glue wire-format header: 1-byte protocol version, 1-byte compression byte,
// followed by the 16-byte binary schema version UUID.
const (
	glueHeaderVersion     = 3
	glueHeaderCompression = 0
	glueHeaderSize        = 18
)

type cacheEntry struct {
	versionIDBytes []byte
	versionIDStr   string
	expiresAt      time.Time
}

type Codec struct {
	client       *glue.Client
	registryName string
	useStub      bool
	cache        map[string]cacheEntry
	mu           sync.RWMutex
	cacheTTL     time.Duration
}

func NewCodec(cfg aws.Config, registryName string, useStub bool, customEndpoint string, cacheTTL time.Duration) *Codec {
	var client *glue.Client
	if !useStub {
		client = glue.NewFromConfig(cfg, func(o *glue.Options) {
			if customEndpoint != "" {
				o.BaseEndpoint = aws.String(customEndpoint)
			}
		})
	}
	if cacheTTL <= 0 {
		cacheTTL = 5 * time.Minute
	}
	return &Codec{
		client:       client,
		registryName: registryName,
		useStub:      useStub,
		cache:        make(map[string]cacheEntry),
		cacheTTL:     cacheTTL,
	}
}

func (c *Codec) Encode(ctx context.Context, schemaName string, payload json.RawMessage) ([]byte, string, error) {
	if c.useStub {
		return payload, "", nil
	}

	versionIDBytes, versionIDStr, err := c.getSchemaVersionID(ctx, schemaName)
	if err != nil {
		return nil, "", fmt.Errorf("resolve schema version id: %w", err)
	}

	encoded := make([]byte, 2+len(versionIDBytes)+len(payload))
	encoded[0] = glueHeaderVersion
	encoded[1] = glueHeaderCompression
	copy(encoded[2:2+len(versionIDBytes)], versionIDBytes)
	copy(encoded[2+len(versionIDBytes):], payload)

	return encoded, versionIDStr, nil
}

// Decode reverses Encode, stripping the Glue wire-format header to recover
// the plain-JSON payload. schemaID is unused: the header already carries
// the schema version UUID, and this codec has no separate lookup need on
// decode.
func (c *Codec) Decode(_ context.Context, _ string, encoded []byte) (json.RawMessage, error) {
	if len(encoded) < glueHeaderSize {
		return nil, fmt.Errorf("glue codec: encoded payload is %d bytes — shorter than the %d-byte Glue header", len(encoded), glueHeaderSize)
	}
	if encoded[0] != glueHeaderVersion {
		return nil, fmt.Errorf("glue codec: unexpected header version byte 0x%02x — want 0x%02x", encoded[0], glueHeaderVersion)
	}
	return json.RawMessage(encoded[glueHeaderSize:]), nil
}

func (c *Codec) getSchemaVersionID(ctx context.Context, schemaName string) ([]byte, string, error) {
	c.mu.RLock()
	entry, ok := c.cache[schemaName]
	c.mu.RUnlock()

	if ok && time.Now().Before(entry.expiresAt) {
		return entry.versionIDBytes, entry.versionIDStr, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok = c.cache[schemaName]
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.versionIDBytes, entry.versionIDStr, nil
	}

	input := &glue.GetSchemaVersionInput{
		SchemaId: &types.SchemaId{
			SchemaName:   aws.String(schemaName),
			RegistryName: aws.String(c.registryName),
		},
		SchemaVersionNumber: &types.SchemaVersionNumber{
			LatestVersion: true,
		},
	}

	result, err := c.client.GetSchemaVersion(ctx, input)
	if err != nil {
		return nil, "", fmt.Errorf("glue GetSchemaVersion: %w", err)
	}

	if result.SchemaVersionId == nil {
		return nil, "", errors.New("glue returned nil schema version id")
	}

	versionIDStr := *result.SchemaVersionId
	parsedUUID, err := uuid.Parse(versionIDStr)
	if err != nil {
		return nil, "", fmt.Errorf("parse schema version id as uuid: %w", err)
	}

	binaryBytes, err := parsedUUID.MarshalBinary()
	if err != nil {
		return nil, "", fmt.Errorf("marshal uuid to binary: %w", err)
	}

	c.cache[schemaName] = cacheEntry{
		versionIDBytes: binaryBytes,
		versionIDStr:   versionIDStr,
		expiresAt:      time.Now().Add(c.cacheTTL),
	}

	return binaryBytes, versionIDStr, nil
}
