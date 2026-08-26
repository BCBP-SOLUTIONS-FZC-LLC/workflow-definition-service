DROP TRIGGER IF EXISTS update_workflow_template_meta ON workflow_template;
DROP TRIGGER IF EXISTS update_workflow_module_version_meta ON workflow_module_version;
DROP TRIGGER IF EXISTS update_workflow_module_meta ON workflow_module;
DROP TRIGGER IF EXISTS update_workflow_version_meta ON workflow_version;
DROP TRIGGER IF EXISTS update_workflow_meta ON workflow;
DROP FUNCTION IF EXISTS update_meta_columns();

DROP TABLE IF EXISTS rls_violation_log;
DROP TABLE IF EXISTS connector_sql_alias;
DROP TABLE IF EXISTS connector_rest_alias;
DROP TABLE IF EXISTS processed_event;
DROP TABLE IF EXISTS workflow_template;
ALTER TABLE workflow_module DROP CONSTRAINT IF EXISTS fk_module_active_version;
DROP TABLE IF EXISTS workflow_module_version;
DROP TABLE IF EXISTS workflow_module;
DROP TABLE IF EXISTS workflow_node_assignee;
ALTER TABLE workflow DROP CONSTRAINT IF EXISTS fk_active_version;
DROP TABLE IF EXISTS workflow_version;
DROP TABLE IF EXISTS workflow;
