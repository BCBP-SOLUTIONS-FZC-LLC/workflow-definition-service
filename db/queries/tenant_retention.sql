-- name: RecordTenantOffboarded :exec
INSERT INTO tenant_retention (tenant_id, offboarded_at, credentials_purge_after, artifacts_purge_after, next_attempt_at)
VALUES ($1, $2, $3, $4, $2)
ON CONFLICT (tenant_id) DO UPDATE SET
    offboarded_at = EXCLUDED.offboarded_at,
    credentials_purge_after = EXCLUDED.credentials_purge_after,
    artifacts_purge_after = EXCLUDED.artifacts_purge_after,
    credentials_purged_at = NULL,
    purged_at = NULL,
    returned_at = NULL,
    attempts = 0,
    next_attempt_at = EXCLUDED.next_attempt_at
WHERE tenant_retention.returned_at IS NOT NULL
  AND tenant_retention.returned_at < EXCLUDED.offboarded_at;

-- name: RecordTenantReturned :exec
INSERT INTO tenant_retention (tenant_id, returned_at, next_attempt_at)
VALUES ($1, $2, $2)
ON CONFLICT (tenant_id) DO UPDATE SET returned_at = EXCLUDED.returned_at
WHERE (tenant_retention.offboarded_at IS NULL OR tenant_retention.offboarded_at < EXCLUDED.returned_at)
  AND (tenant_retention.returned_at IS NULL OR tenant_retention.returned_at < EXCLUDED.returned_at);

-- name: LockCurrentTenantRetention :one
SELECT record_version FROM tenant_retention
WHERE tenant_id = $1 AND record_version = $2 AND returned_at IS NULL
FOR SHARE;

-- name: ListTenantsDueForPurge :many
SELECT
    tenant_id,
    (credentials_purged_at IS NULL AND credentials_purge_after <= $1)::boolean AS credentials_due,
    (purged_at IS NULL AND artifacts_purge_after <= $1)::boolean AS artifacts_due,
    attempts,
    record_version
FROM tenant_retention
WHERE next_attempt_at <= $1
  AND returned_at IS NULL
  AND ((credentials_purged_at IS NULL AND credentials_purge_after <= $1)
    OR (purged_at IS NULL AND artifacts_purge_after <= $1))
ORDER BY LEAST(credentials_purge_after, artifacts_purge_after), tenant_id
LIMIT $2;

-- name: MarkTenantCredentialsPurged :one
UPDATE tenant_retention SET credentials_purged_at = $3, attempts = 0
WHERE tenant_id = $1 AND record_version = $2
RETURNING record_version;

-- name: MarkTenantPurged :one
UPDATE tenant_retention SET purged_at = $3, attempts = 0
WHERE tenant_id = $1 AND record_version = $2
RETURNING record_version;

-- name: RecordTenantPurgeFailure :one
UPDATE tenant_retention SET attempts = $3, next_attempt_at = $4
WHERE tenant_id = $1 AND record_version = $2
RETURNING record_version;

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
