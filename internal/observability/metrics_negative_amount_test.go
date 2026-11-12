package observability_test

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

func TestAddRLSViolations_IgnoresANegativeAmountInsteadOfPanicking(t *testing.T) {
	initIdentity(t)
	counter := observability.RLSViolationsTotal.WithLabelValues("cross_tenant_access")
	observability.AddRLSViolations("cross_tenant_access", 2)
	before := testutil.ToFloat64(counter)

	require.NotPanics(t, func() { observability.AddRLSViolations("cross_tenant_access", -1) })

	require.InDelta(t, before, testutil.ToFloat64(counter), 0)
}

func TestAddTenantPurgeRows_IgnoresANegativeAmountInsteadOfPanicking(t *testing.T) {
	initIdentity(t)
	counter := observability.TenantPurgeRowsDeletedTotal.WithLabelValues("negative_amount")
	observability.AddTenantPurgeRows("negative_amount", 2)
	before := testutil.ToFloat64(counter)

	require.NotPanics(t, func() { observability.AddTenantPurgeRows("negative_amount", -5) })

	require.InDelta(t, before, testutil.ToFloat64(counter), 0)
}

func TestAddRLSViolationsAndTenantPurgeRows_AddZeroWithoutChangingTheCount(t *testing.T) {
	initIdentity(t)
	rls := observability.RLSViolationsTotal.WithLabelValues("cross_tenant_access")
	purge := observability.TenantPurgeRowsDeletedTotal.WithLabelValues("zero_amount")
	rlsBefore := testutil.ToFloat64(rls)
	observability.AddTenantPurgeRows("zero_amount", 4)
	purgeBefore := testutil.ToFloat64(purge)

	observability.AddRLSViolations("cross_tenant_access", 0)
	observability.AddTenantPurgeRows("zero_amount", 0)

	require.InDelta(t, rlsBefore, testutil.ToFloat64(rls), 0)
	require.InDelta(t, purgeBefore, testutil.ToFloat64(purge), 0)
}
