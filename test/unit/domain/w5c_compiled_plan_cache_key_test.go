package domain_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestCompiledPlanCacheKey_IsTheWfPlanPrefixWithTenantThenVersion(t *testing.T) {
	tenantA, tenantB, version := uuid.New(), uuid.New(), uuid.New()

	keyA := domain.CompiledPlanCacheKey(tenantA, version)
	keyB := domain.CompiledPlanCacheKey(tenantB, version)

	assert.Equal(t, "wf:plan:"+tenantA.String()+":"+version.String(), keyA)
	assert.Equal(t, "wf:plan:"+tenantB.String()+":"+version.String(), keyB)
	assert.NotEqual(t, keyA, keyB, "the same version id under two tenants gives two keys")
	assert.NotEqual(t, domain.CompiledPlanCacheKey(tenantA, tenantB), domain.CompiledPlanCacheKey(tenantB, tenantA), "tenant and version are not interchangeable")
}
