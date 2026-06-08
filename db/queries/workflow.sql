-- name: CreateWorkflow :exec
INSERT INTO workflow (id, tenant_id, created_by_user_id, business_key, name, description, active_version_id)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetWorkflowByID :one
SELECT id, tenant_id, created_by_user_id, business_key, name, description, active_version_id, created_at, updated_at
FROM workflow
WHERE tenant_id = $1 AND id = $2;

-- name: GetWorkflowByBusinessKey :one
SELECT id, tenant_id, created_by_user_id, business_key, name, description, active_version_id, created_at, updated_at
FROM workflow
WHERE tenant_id = $1 AND business_key = $2;

-- name: ListWorkflows :many
SELECT id, tenant_id, created_by_user_id, business_key, name, description, active_version_id, created_at, updated_at
FROM workflow
WHERE tenant_id = $1
ORDER BY created_at DESC
LIMIT $2
OFFSET $3;

-- name: CountWorkflowsByTenant :one
SELECT COUNT(*) FROM workflow WHERE tenant_id = $1;

-- name: UpdateActiveVersion :execresult
UPDATE workflow
SET active_version_id = $3, updated_at = now()
WHERE tenant_id = $1 AND id = $2;
