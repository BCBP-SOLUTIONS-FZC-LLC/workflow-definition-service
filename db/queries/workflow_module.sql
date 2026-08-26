-- name: CreateModule :exec
INSERT INTO workflow_module (id, tenant_id, scope, name, description, created_by_user_id)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetModuleByID :one
SELECT id, tenant_id, scope, name, description, active_version_id,
       created_by_user_id, created_at, updated_at, record_version
FROM workflow_module
WHERE id = $1 AND (scope = 'global' OR tenant_id = $2);

-- name: UpdateModuleActiveVersion :execresult
UPDATE workflow_module SET active_version_id = $2, updated_at = now() WHERE id = $1;
