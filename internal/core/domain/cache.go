package domain

import "github.com/google/uuid"

// CompiledPlanCacheKey is the Valkey key for a version's cached gRPC
// GetCompiledWorkflow response: wf:plan:<tenantID>:<versionID>. It is populated
// lazily on the first gRPC read and deleted when the version's status or
// is_valid changes (archive, membership-revocation invalidation).
func CompiledPlanCacheKey(tenantID, versionID uuid.UUID) string {
	return "wf:plan:" + tenantID.String() + ":" + versionID.String()
}
