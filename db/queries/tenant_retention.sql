-- name: RecordTenantOffboarded :exec
INSERT INTO tenant_retention (tenant_id, offboarded_at, credentials_purge_after, artifacts_purge_after, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (tenant_id) DO NOTHING;

-- name: ListTenantsDueForPurge :many
SELECT
    tenant_id,
    (credentials_purged_at IS NULL AND credentials_purge_after <= $1)::boolean AS credentials_due,
    (purged_at IS NULL AND artifacts_purge_after <= $1)::boolean AS artifacts_due
FROM tenant_retention
WHERE (credentials_purged_at IS NULL AND credentials_purge_after <= $1)
   OR (purged_at IS NULL AND artifacts_purge_after <= $1)
ORDER BY LEAST(credentials_purge_after, artifacts_purge_after), tenant_id
LIMIT $2;

-- name: MarkTenantCredentialsPurged :exec
UPDATE tenant_retention SET credentials_purged_at = $2, updated_at = now() WHERE tenant_id = $1;

-- name: MarkTenantPurged :exec
UPDATE tenant_retention SET purged_at = $2, updated_at = now() WHERE tenant_id = $1;

-- name: PurgeTenantWorkflows :execrows
DELETE FROM workflow
WHERE id IN (SELECT w.id FROM workflow w WHERE w.tenant_id = $1 LIMIT $2);

-- name: PurgeTenantModules :execrows
DELETE FROM workflow_module
WHERE id IN (SELECT m.id FROM workflow_module m WHERE m.tenant_id = $1 AND m.scope = 'tenant' LIMIT $2);

-- name: PurgeTenantStarters :execrows
DELETE FROM workflow_template
WHERE id IN (SELECT t.id FROM workflow_template t WHERE t.tenant_id = $1 AND t.scope = 'tenant' LIMIT $2);

-- name: TenantHasRows :one
SELECT (
    EXISTS (SELECT 1 FROM workflow w WHERE w.tenant_id = $1)
    OR EXISTS (SELECT 1 FROM workflow_module m WHERE m.tenant_id = $1 AND m.scope = 'tenant')
    OR EXISTS (SELECT 1 FROM workflow_template t WHERE t.tenant_id = $1 AND t.scope = 'tenant')
)::boolean AS has_rows;
