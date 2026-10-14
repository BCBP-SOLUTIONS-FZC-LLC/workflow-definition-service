-- The caller's tenant, or NULL when app.tenant_id is unset or not a UUID. A
-- regex guard replaces a cast inside an exception block: that block opens a
-- subtransaction for every row a policy checks, and past 64 of them in one
-- transaction every concurrent reader slows down.
CREATE OR REPLACE FUNCTION rls_app_tenant()
RETURNS uuid
LANGUAGE sql
STABLE
PARALLEL SAFE
SET search_path = public
AS $$
    SELECT CASE
        WHEN current_setting('app.tenant_id', true) ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        THEN current_setting('app.tenant_id', true)::uuid
    END
$$;

-- Tenant checks with sampled violation logging. A row that fails a plain USING
-- clause is silently filtered; these functions record 1% of such rows so a pool
-- connection with no tenant GUC, a cross-tenant bug or a probe leaves a signal.
CREATE OR REPLACE FUNCTION rls_check_tenant(row_tenant_id uuid, tbl text)
RETURNS boolean
LANGUAGE plpgsql
STRICT
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
    app_tenant uuid;
BEGIN
    app_tenant := rls_app_tenant();

    IF row_tenant_id = app_tenant THEN
        RETURN true;
    END IF;

    IF random() < 0.01 THEN
        INSERT INTO rls_violation_log (violation_type, app_tenant_id, row_tenant_id, table_name, query_text)
        VALUES (
            CASE WHEN app_tenant IS NULL THEN 'missing_or_invalid_guc' ELSE 'cross_tenant_access' END,
            app_tenant,
            row_tenant_id,
            tbl,
            current_query()
        );
    END IF;

    RETURN false;
END;
$$;

-- Not STRICT: a scope='global' row has a NULL tenant_id, and a STRICT function
-- would return NULL (false in a USING clause) for it, hiding every global row.
CREATE OR REPLACE FUNCTION rls_check_tenant_or_global(row_tenant_id uuid, row_scope catalog_scope, tbl text)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
DECLARE
    app_tenant uuid;
BEGIN
    IF row_scope = 'global' THEN
        RETURN true;
    END IF;

    app_tenant := rls_app_tenant();

    IF row_tenant_id = app_tenant THEN
        RETURN true;
    END IF;

    IF random() < 0.01 THEN
        INSERT INTO rls_violation_log (violation_type, app_tenant_id, row_tenant_id, table_name, query_text)
        VALUES (
            CASE WHEN app_tenant IS NULL THEN 'missing_or_invalid_guc' ELSE 'cross_tenant_access' END,
            app_tenant,
            row_tenant_id,
            tbl,
            current_query()
        );
    END IF;

    RETURN false;
END;
$$;

REVOKE ALL ON workflow FROM PUBLIC;
ALTER TABLE workflow ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow
    USING (rls_check_tenant(tenant_id, 'workflow'));

REVOKE ALL ON workflow_version FROM PUBLIC;
ALTER TABLE workflow_version ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_version FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_version
    USING (rls_check_tenant(tenant_id, 'workflow_version'));

REVOKE ALL ON workflow_node_assignee FROM PUBLIC;
ALTER TABLE workflow_node_assignee ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_node_assignee FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_node_assignee
    USING (rls_check_tenant(tenant_id, 'workflow_node_assignee'));

-- WITH CHECK only allows scope='tenant' rows through this policy, so the
-- ordinary RLS-bound app role can never insert a scope='global' row no matter
-- what the application code does — only a BYPASSRLS system-role connection can.
REVOKE ALL ON workflow_module FROM PUBLIC;
ALTER TABLE workflow_module ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_module FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_module
    USING      (rls_check_tenant_or_global(tenant_id, scope, 'workflow_module'))
    WITH CHECK (scope = 'tenant' AND tenant_id = current_setting('app.tenant_id', true)::uuid);

REVOKE ALL ON workflow_module_version FROM PUBLIC;
ALTER TABLE workflow_module_version ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_module_version FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_module_version
    USING      (rls_check_tenant_or_global(tenant_id, scope, 'workflow_module_version'))
    WITH CHECK (scope = 'tenant' AND tenant_id = current_setting('app.tenant_id', true)::uuid);

REVOKE ALL ON workflow_template FROM PUBLIC;
ALTER TABLE workflow_template ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_template FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_template
    USING      (rls_check_tenant_or_global(tenant_id, scope, 'workflow_template'))
    WITH CHECK (scope = 'tenant' AND tenant_id = current_setting('app.tenant_id', true)::uuid);

-- connector_rest_alias is an org-owned internal service config table, not
-- tenant data — no RLS here, matches rls_violation_log.
