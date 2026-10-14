-- name: CreateModuleVersion :exec
INSERT INTO workflow_module_version (
    id, module_id, tenant_id, scope, status, bpmn_xml, process_id,
    version_number, is_valid, validation_errors_json, created_by_user_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: GetModuleVersionByID :one
SELECT id, module_id, tenant_id, scope, status, bpmn_xml, process_id,
       version_number, is_valid, validation_errors_json, published_at,
       created_by_user_id, created_at, updated_at, record_version
FROM workflow_module_version
WHERE id = $1 AND (scope = 'global' OR tenant_id = $2);

-- name: ListModuleVersionsByModule :many
SELECT id, module_id, tenant_id, scope, status, bpmn_xml, process_id,
       version_number, is_valid, validation_errors_json, published_at,
       created_by_user_id, created_at, updated_at, record_version
FROM workflow_module_version
WHERE module_id = $1 AND (scope = 'global' OR tenant_id = $2)
ORDER BY created_at DESC
LIMIT $3
OFFSET $4;

-- name: CountModuleVersionsByModule :one
SELECT COUNT(*) FROM workflow_module_version
WHERE module_id = $1 AND (scope = 'global' OR tenant_id = $2);

-- name: PublishModuleVersion :execresult
UPDATE workflow_module_version
SET status = 'PUBLISHED', version_number = sqlc.arg(version_number), published_at = now()
WHERE id = sqlc.arg(id) AND status = 'DRAFT'
  AND (scope = 'global' OR tenant_id = sqlc.arg(tenant_id));

-- name: ArchiveModuleVersion :execresult
UPDATE workflow_module_version SET status = 'ARCHIVED'
WHERE id = sqlc.arg(id) AND status = 'PUBLISHED'
  AND (scope = 'global' OR tenant_id = sqlc.arg(tenant_id));

-- name: NextModuleVersionNumber :one
SELECT COALESCE(MAX(version_number), 0) + 1
FROM workflow_module_version WHERE module_id = $1 AND status != 'DRAFT';

-- name: ResolveModuleVersion :one
-- The version a call names: sqlc.narg(version_number), or the module's active
-- version when NULL. An archived module (no active version) resolves nothing.
SELECT v.id, v.module_id, v.tenant_id, v.scope, v.status, v.bpmn_xml, v.process_id,
       v.version_number, v.is_valid, v.validation_errors_json, v.published_at,
       v.created_by_user_id, v.created_at, v.updated_at, v.record_version
FROM workflow_module m
JOIN workflow_module_version v ON v.module_id = m.id
WHERE m.process_id = sqlc.arg(process_id)
  AND (m.scope = 'global' OR m.tenant_id = sqlc.arg(tenant_id))
  AND m.active_version_id IS NOT NULL
  AND v.version_number IS NOT NULL
  AND (
      (sqlc.narg(version_number)::int IS NULL AND v.id = m.active_version_id)
      OR v.version_number = sqlc.narg(version_number)::int
  )
-- Only a race between two creates gives a tenant module a global module's
-- process id; the tenant's own is then the one it meant.
ORDER BY (m.scope = 'tenant') DESC
LIMIT 1;
