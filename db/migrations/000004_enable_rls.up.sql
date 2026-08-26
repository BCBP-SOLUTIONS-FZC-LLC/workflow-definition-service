ALTER TABLE workflow ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow
    USING (tenant_id = current_setting('app.tenant_id')::uuid);

ALTER TABLE workflow_version ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_version
    USING (tenant_id = current_setting('app.tenant_id')::uuid);

ALTER TABLE workflow_node_assignee ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_node_assignee
    USING (tenant_id = current_setting('app.tenant_id')::uuid);

-- WITH CHECK only allows scope='tenant' rows through this policy, so the
-- ordinary RLS-bound app role can never insert a scope='global' row no matter
-- what the application code does — only a BYPASSRLS system-role connection can.
ALTER TABLE workflow_module ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_module
    USING      (scope = 'global' OR tenant_id = current_setting('app.tenant_id', true)::uuid)
    WITH CHECK (scope = 'tenant' AND tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE workflow_module_version ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_module_version
    USING      (scope = 'global' OR tenant_id = current_setting('app.tenant_id', true)::uuid)
    WITH CHECK (scope = 'tenant' AND tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE workflow_template ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_policy ON workflow_template
    USING      (scope = 'global' OR tenant_id = current_setting('app.tenant_id', true)::uuid)
    WITH CHECK (scope = 'tenant' AND tenant_id = current_setting('app.tenant_id', true)::uuid);

-- processed_event carries no tenant_id (operational dedup log, not tenant
-- data, composite (event_id, consumer) PK) — no RLS here, matches rls_violation_log.
-- connector_rest_alias/connector_sql_alias are org-owned internal service
-- config, not tenant data either — same reasoning, no RLS.
