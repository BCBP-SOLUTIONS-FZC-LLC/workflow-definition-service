package main

import (
	"context"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/pkg/pgcommon"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
)

// dbPinger adapts this binary's concrete *pgcommon.Pool to httpadapter's
// DBPinger interface — the composition root's job, per that package's own
// router.go doc comment, so it never imports pgcommon directly.
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
