package main

import (
	"context"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
)

func checkPoolRoles(app, sys pgadapter.RolePosture, appEnv string, systemConfigured bool) ([]string, error) {
	if app.BypassesRLS() {
		return nil, fmt.Errorf("the application database role %q bypasses row-level security (superuser=%t, bypassrls=%t): "+
			"tenant isolation would rest on application code alone; connect DATABASE_URL as a NOSUPERUSER NOBYPASSRLS role",
			app.Role, app.Superuser, app.BypassRLS)
	}
	devLike := appEnv == "dev" || appEnv == "local" || appEnv == "test"
	strict := !devLike || systemConfigured

	var warnings []string
	if !sys.BypassesRLS() {
		msg := fmt.Sprintf("the system database role %q cannot bypass row-level security: global module and starter writes would see no rows", sys.Role)
		if strict {
			return nil, fmt.Errorf("%s; point SYSTEM_DATABASE_URL at the BYPASSRLS role", msg)
		}
		warnings = append(warnings, msg+" (dev, SYSTEM_DATABASE_URL unset)")
	}
	if !sys.CanWrite {
		msg := fmt.Sprintf("the system database role %q cannot write the module, starter and violation-log tables", sys.Role)
		if strict {
			return nil, fmt.Errorf("%s; SYSTEM_DATABASE_URL needs a BYPASSRLS role that can write, not a read-only one", msg)
		}
		warnings = append(warnings, msg+" (dev, SYSTEM_DATABASE_URL unset)")
	}
	return warnings, nil
}

func verifyPoolRoles(ctx context.Context, appPool, systemPool *pgcommon.Pool, appEnv string, systemConfigured bool) ([]string, pgadapter.RolePosture, pgadapter.RolePosture, error) {
	app, err := pgadapter.PoolRolePosture(ctx, appPool)
	if err != nil {
		return nil, app, app, fmt.Errorf("read the application pool's role: %w", err)
	}
	sys, err := pgadapter.PoolRolePosture(ctx, systemPool)
	if err != nil {
		return nil, app, sys, fmt.Errorf("read the system pool's role: %w", err)
	}
	warnings, err := checkPoolRoles(app, sys, appEnv, systemConfigured)
	return warnings, app, sys, err
}
