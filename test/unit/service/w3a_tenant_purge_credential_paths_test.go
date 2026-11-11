package service_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/v2/pkg/registry"
)

func TestTenantPurger_DeletesExactlyTheSecretFieldPathsOfEachConnectorInSortedOrder(t *testing.T) {
	tenant := uuid.New()
	r := &fakeRetention{due: bothDue(tenant)}
	d := &fakeDataPurger{}
	s := &fakeSecrets{}
	rec, failed := map[string]int64{}, 0

	require.NoError(t, newPurger(r, d, s, rec, &failed).Tick(context.Background()))

	var want []string
	for connectorType, def := range registry.All() {
		for _, f := range def.Inputs {
			if f.Kind == registry.FieldKindSecretRef {
				want = append(want, fmt.Sprintf("connectors/%s/%s/%s", tenant, connectorType, f.Name))
			}
		}
	}
	slices.Sort(want)
	require.NotEmpty(t, want, "the registry defines connectors with secret fields")

	assert.Equal(t, want, s.deleted, "credential paths are deleted in sorted order, one per secret field of each connector type")
	assert.Contains(t, s.deleted, "connectors/"+tenant.String()+"/send-email/apiKey")
	assert.Zero(t, failed)
}
