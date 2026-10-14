-- name: CreateModule :exec
INSERT INTO workflow_module (id, tenant_id, scope, name, description, process_id, created_by_user_id)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetModuleByID :one
SELECT id, tenant_id, scope, name, description, process_id, active_version_id,
       created_by_user_id, created_at, updated_at, record_version
FROM workflow_module
WHERE id = $1 AND (scope = 'global' OR tenant_id = $2);

-- name: UpdateModuleActiveVersion :execresult
-- The pointer may only name a PUBLISHED version of this module, or be cleared.
UPDATE workflow_module AS m
SET active_version_id = sqlc.narg(active_version_id), updated_at = now()
WHERE m.id = sqlc.arg(id)
  AND (sqlc.narg(active_version_id)::uuid IS NULL OR EXISTS (
        SELECT 1 FROM workflow_module_version v
        WHERE v.id = sqlc.narg(active_version_id) AND v.module_id = m.id AND v.status = 'PUBLISHED'));

-- name: ModuleProcessIDInUse :one
-- A NULL tenant, used on the system pool when creating a global module,
-- checks every module; otherwise the tenant's own and the global ones.
SELECT EXISTS (
    SELECT 1 FROM workflow_module
    WHERE process_id = sqlc.arg(process_id)
      AND (sqlc.narg(tenant_id)::uuid IS NULL OR scope = 'global' OR tenant_id = sqlc.narg(tenant_id)::uuid)
);
