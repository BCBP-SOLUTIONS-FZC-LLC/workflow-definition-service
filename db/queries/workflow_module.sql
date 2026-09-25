-- name: CreateModule :exec
INSERT INTO workflow_module (id, tenant_id, scope, name, description, process_id, created_by_user_id)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetModuleByID :one
SELECT id, tenant_id, scope, name, description, process_id, active_version_id,
       created_by_user_id, created_at, updated_at, record_version
FROM workflow_module
WHERE id = $1 AND (scope = 'global' OR tenant_id = $2);

-- name: UpdateModuleActiveVersion :execresult
UPDATE workflow_module SET active_version_id = $2, updated_at = now() WHERE id = $1;

-- name: ModuleProcessIDInUse :one
-- A NULL tenant, used on the system pool when creating a global module,
-- checks every module; otherwise the tenant's own and the global ones.
SELECT EXISTS (
    SELECT 1 FROM workflow_module
    WHERE process_id = sqlc.arg(process_id)
      AND (sqlc.narg(tenant_id)::uuid IS NULL OR scope = 'global' OR tenant_id = sqlc.narg(tenant_id)::uuid)
);
