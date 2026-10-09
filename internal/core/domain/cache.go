package domain

import "github.com/google/uuid"

func CompiledPlanCacheKey(tenantID, versionID uuid.UUID) string {
	return "wf:plan:" + tenantID.String() + ":" + versionID.String()
}
