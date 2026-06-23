package port

import (
	"context"
)

type GlueCodec interface {
	Encode(ctx context.Context, schemaName string, payload []byte) ([]byte, error)
}
