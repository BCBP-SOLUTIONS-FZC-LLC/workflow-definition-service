package glue

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	"github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.GlueCodec = (*Codec)(nil)

type cacheEntry struct {
	versionIDBytes []byte
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

func NewCodec(cfg aws.Config, registryName string, useStub bool, customEndpoint string) *Codec {
	var client *glue.Client
	if !useStub {
		client = glue.NewFromConfig(cfg, func(o *glue.Options) {
			if customEndpoint != "" {
				o.BaseEndpoint = aws.String(customEndpoint)
			}
		})
	}
	return &Codec{
		client:       client,
		registryName: registryName,
		useStub:      useStub,
		cache:        make(map[string]cacheEntry),
		cacheTTL:     5 * time.Minute,
	}
}

func (c *Codec) Encode(ctx context.Context, schemaName string, payload []byte) ([]byte, error) {
	if c.useStub {
		return payload, nil
	}

	versionIDBytes, err := c.getSchemaVersionIDBytes(ctx, schemaName)
	if err != nil {
		return nil, fmt.Errorf("resolve schema version id: %w", err)
	}

	encoded := make([]byte, 2+len(versionIDBytes)+len(payload))
	encoded[0] = 3
	encoded[1] = 0
	copy(encoded[2:2+len(versionIDBytes)], versionIDBytes)
	copy(encoded[2+len(versionIDBytes):], payload)

	return encoded, nil
}

func (c *Codec) getSchemaVersionIDBytes(ctx context.Context, schemaName string) ([]byte, error) {
	c.mu.RLock()
	entry, ok := c.cache[schemaName]
	c.mu.RUnlock()

	if ok && time.Now().Before(entry.expiresAt) {
		return entry.versionIDBytes, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok = c.cache[schemaName]
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.versionIDBytes, nil
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
		return nil, fmt.Errorf("glue GetSchemaVersion: %w", err)
	}

	if result.SchemaVersionId == nil {
		return nil, errors.New("glue returned nil schema version id")
	}

	versionIDStr := *result.SchemaVersionId
	parsedUUID, err := uuid.Parse(versionIDStr)
	if err != nil {
		return nil, fmt.Errorf("parse schema version id as uuid: %w", err)
	}

	binaryBytes, err := parsedUUID.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("marshal uuid to binary: %w", err)
	}

	c.cache[schemaName] = cacheEntry{
		versionIDBytes: binaryBytes,
		expiresAt:      time.Now().Add(c.cacheTTL),
	}

	return binaryBytes, nil
}
