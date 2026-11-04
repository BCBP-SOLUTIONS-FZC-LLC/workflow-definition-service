package postgres

import (
	"context"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RolePosture struct {
	Role      string
	Superuser bool
	BypassRLS bool
	CanWrite  bool
}

func (p RolePosture) BypassesRLS() bool { return p.Superuser || p.BypassRLS }

func PoolRolePosture(ctx context.Context, pool *pgcommon.Pool) (RolePosture, error) {
	var p RolePosture
	err := pool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx,
			`SELECT current_user, r.rolsuper, r.rolbypassrls,
			        CASE WHEN to_regclass('public.workflow_module') IS NULL THEN true
			             ELSE has_table_privilege('public.workflow_module', 'UPDATE') END
			    AND CASE WHEN to_regclass('public.workflow_module_version') IS NULL THEN true
			             ELSE has_table_privilege('public.workflow_module_version', 'UPDATE') END
			    AND CASE WHEN to_regclass('public.workflow_template') IS NULL THEN true
			             ELSE has_table_privilege('public.workflow_template', 'UPDATE') END
			    AND CASE WHEN to_regclass('public.rls_violation_log') IS NULL THEN true
			             ELSE has_table_privilege('public.rls_violation_log', 'DELETE') END
			   FROM pg_roles r WHERE r.rolname = current_user`,
		).Scan(&p.Role, &p.Superuser, &p.BypassRLS, &p.CanWrite)
	})
	return p, asUnavailable(err)
}
