DROP POLICY IF EXISTS tenant_isolation_policy ON workflow_template;
ALTER TABLE workflow_template DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation_policy ON workflow_module_version;
ALTER TABLE workflow_module_version DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation_policy ON workflow_module;
ALTER TABLE workflow_module DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation_policy ON workflow_node_assignee;
ALTER TABLE workflow_node_assignee DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation_policy ON workflow_version;
ALTER TABLE workflow_version DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation_policy ON workflow;
ALTER TABLE workflow DISABLE ROW LEVEL SECURITY;
