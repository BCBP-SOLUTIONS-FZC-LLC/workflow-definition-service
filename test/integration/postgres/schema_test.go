//go:build integration

package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func schemaRows(t *testing.T, query string, args ...any) []string {
	t.Helper()
	pool := fixtures.NewTestPool(t)
	var out []string
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		rows, err := conn.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSchema_EveryTimeColumnIsTimestamptz(t *testing.T) {
	got := schemaRows(t, `SELECT table_name || '.' || column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND data_type = 'timestamp without time zone' ORDER BY 1`)
	if len(got) > 0 {
		t.Errorf("columns without a time zone: %s", strings.Join(got, ", "))
	}
}

func TestSchema_AuditColumnsAreNotNull(t *testing.T) {
	got := schemaRows(t, `SELECT table_name || '.' || column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name IN ('created_at', 'updated_at') AND is_nullable = 'YES'
		AND table_name IN ('workflow', 'workflow_version', 'workflow_module', 'workflow_module_version', 'workflow_template', 'workflow_node_assignee') ORDER BY 1`)
	if len(got) > 0 {
		t.Errorf("nullable audit columns: %s", strings.Join(got, ", "))
	}
}

func TestSchema_ParentKeysAreNotNull(t *testing.T) {
	got := schemaRows(t, `SELECT table_name || '.' || column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND is_nullable = 'YES'
		AND (table_name, column_name) IN (('workflow_version', 'workflow_id'), ('workflow_module_version', 'module_id'), ('workflow_node_assignee', 'workflow_version_id')) ORDER BY 1`)
	if len(got) > 0 {
		t.Errorf("nullable parent keys: %s", strings.Join(got, ", "))
	}
}

func execSuper(t *testing.T, pool interface {
	WithConn(context.Context, func(context.Context, *pgxpool.Conn) error) error
}, sql string, args ...any) error {
	t.Helper()
	return pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx, sql, args...)
		return err
	})
}

func TestSchema_AChildCannotNameAnotherTenantsParent(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	tenantA, tenantB, wf, user := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := execSuper(t, pool, `INSERT INTO workflow (id, tenant_id, created_by_user_id, template_key, name) VALUES ($1, $2, $3, 'k', 'n')`, wf, tenantA, user); err != nil {
		t.Fatal(err)
	}
	version := `INSERT INTO workflow_version (id, workflow_id, tenant_id, status, bpmn_xml, created_by_user_id) VALUES ($1, $2, $3, 'DRAFT', '<x/>', $4)`

	if err := execSuper(t, pool, version, uuid.New(), wf, tenantB, user); err == nil {
		t.Error("a version of tenant B was attached to tenant A's workflow")
	}
	verA := uuid.New()
	if err := execSuper(t, pool, version, verA, wf, tenantA, user); err != nil {
		t.Fatalf("the owning tenant's version was refused: %v", err)
	}
	assignee := `INSERT INTO workflow_node_assignee (id, tenant_id, workflow_version_id, node_key, user_id, department_id, role) VALUES ($1, $2, $3, 'n', $4, $5, 'r')`
	if err := execSuper(t, pool, assignee, uuid.New(), tenantB, verA, user, uuid.New()); err == nil {
		t.Error("an assignee of tenant B was attached to tenant A's version")
	}
	if err := execSuper(t, pool, assignee, uuid.New(), tenantA, verA, user, uuid.New()); err != nil {
		t.Fatalf("the owning tenant's assignee was refused: %v", err)
	}
}

func TestSchema_ARecordVersionOfZeroIsRefused(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	err := execSuper(t, pool, `INSERT INTO workflow (id, tenant_id, created_by_user_id, template_key, name, record_version) VALUES ($1, $2, $3, 'k', 'n', 0)`, uuid.New(), uuid.New(), uuid.New())
	if err == nil {
		t.Error("record_version 0 was accepted")
	}
}

func TestSchema_DeletingAVersionClearsOnlyTheActivePointer(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	tenant, wf, ver, user := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := execSuper(t, pool, `INSERT INTO workflow (id, tenant_id, created_by_user_id, template_key, name) VALUES ($1, $2, $3, 'k', 'n')`, wf, tenant, user); err != nil {
		t.Fatal(err)
	}
	if err := execSuper(t, pool, `INSERT INTO workflow_version (id, workflow_id, tenant_id, status, bpmn_xml, created_by_user_id, version_number) VALUES ($1, $2, $3, 'PUBLISHED', '<x/>', $4, 1)`, ver, wf, tenant, user); err != nil {
		t.Fatal(err)
	}
	if err := execSuper(t, pool, `UPDATE workflow SET active_version_id = $1 WHERE id = $2`, ver, wf); err != nil {
		t.Fatal(err)
	}
	if err := execSuper(t, pool, `DELETE FROM workflow_version WHERE id = $1`, ver); err != nil {
		t.Fatal(err)
	}
	var active *uuid.UUID
	var gotTenant uuid.UUID
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT active_version_id, tenant_id FROM workflow WHERE id = $1`, wf).Scan(&active, &gotTenant)
	})
	if err != nil {
		t.Fatal(err)
	}
	if active != nil || gotTenant != tenant {
		t.Errorf("active_version_id = %v, tenant_id = %v, want NULL and %v", active, gotTenant, tenant)
	}
}

func TestSchema_AModuleVersionHasTheScopeOfItsModule(t *testing.T) {
	pool := fixtures.NewTestPool(t)
	tenant, tenantModule, globalModule := uuid.New(), uuid.New(), uuid.New()
	if err := execSuper(t, pool, `INSERT INTO workflow_module (id, tenant_id, scope, name, process_id) VALUES ($1, $2, 'tenant', 'm', 'p1')`, tenantModule, tenant); err != nil {
		t.Fatal(err)
	}
	if err := execSuper(t, pool, `INSERT INTO workflow_module (id, tenant_id, scope, name, process_id) VALUES ($1, NULL, 'global', 'g', 'p2')`, globalModule); err != nil {
		t.Fatal(err)
	}
	version := `INSERT INTO workflow_module_version (id, module_id, tenant_id, scope, status, bpmn_xml, process_id) VALUES ($1, $2, $3, $4, 'DRAFT', '<x/>', 'p')`

	if err := execSuper(t, pool, version, uuid.New(), tenantModule, nil, "global"); err == nil {
		t.Error("a global version was attached to a tenant module")
	}
	if err := execSuper(t, pool, version, uuid.New(), globalModule, tenant, "tenant"); err == nil {
		t.Error("a tenant version was attached to a global module")
	}
	if err := execSuper(t, pool, version, uuid.New(), tenantModule, tenant, "tenant"); err != nil {
		t.Errorf("the module's own tenant version was refused: %v", err)
	}
	if err := execSuper(t, pool, version, uuid.New(), globalModule, nil, "global"); err != nil {
		t.Errorf("a global module's global version was refused: %v", err)
	}
}
