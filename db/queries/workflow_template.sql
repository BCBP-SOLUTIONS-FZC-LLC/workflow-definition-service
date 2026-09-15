-- name: CreateStarter :exec
INSERT INTO workflow_template
    (id, tenant_id, scope, name, description, category, bpmn_xml, source_workflow_version_id, created_by_user_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetStarterByID :one
SELECT id, tenant_id, scope, name, description, category, bpmn_xml,
       source_workflow_version_id, created_by_user_id, created_at, updated_at, record_version
FROM workflow_template
WHERE id = $1 AND (scope = 'global' OR tenant_id = $2);

-- name: DeleteStarter :execresult
DELETE FROM workflow_template WHERE id = $1;
