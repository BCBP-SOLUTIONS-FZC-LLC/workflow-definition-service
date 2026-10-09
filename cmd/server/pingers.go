package main

import (
	"context"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
)

type dbPinger struct{ pool *pgcommon.Pool }

func (d dbPinger) Health(ctx context.Context) httpadapter.DBHealth {
	hs := d.pool.Health(ctx)
	return httpadapter.DBHealth{
		Healthy:       hs.Healthy,
		Utilization:   hs.Utilization,
		AcquiredConns: hs.AcquiredConns,
		MaxConns:      hs.MaxConns,
	}
}
