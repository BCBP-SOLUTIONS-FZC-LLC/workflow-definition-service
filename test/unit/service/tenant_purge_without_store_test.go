package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTenantPurger_WithoutASecretsStore_LeavesTheCredentialStepPendingForALaterRun(t *testing.T) {
	tenant := uuid.New()
	r := &fakeRetention{due: bothDue(tenant)}
	rec, failed := map[string]int64{}, 0

	require.NoError(t, newPurger(r, &fakeDataPurger{}, nil, rec, &failed).Tick(context.Background()))

	assert.Empty(t, r.credsDone, "no secret was deleted, so the credential step is not recorded as done")
	assert.Equal(t, []uuid.UUID{tenant}, r.purged, "the planning data is still purged and recorded")
	assert.Zero(t, failed, "a missing store is not a purge failure")
	assert.Empty(t, r.failures)
}

func TestTenantPurger_ALaterRunWithAStore_DeletesTheCredentialsTheEarlierRunLeftPending(t *testing.T) {
	tenant := uuid.New()
	r := &fakeRetention{due: bothDue(tenant)}
	rec, failed := map[string]int64{}, 0
	require.NoError(t, newPurger(r, &fakeDataPurger{}, nil, rec, &failed).Tick(context.Background()))
	require.Empty(t, r.credsDone)

	s := &fakeSecrets{}
	require.NoError(t, newPurger(r, &fakeDataPurger{}, s, rec, &failed).Tick(context.Background()))

	assert.Equal(t, []uuid.UUID{tenant}, r.credsDone)
	assert.Contains(t, s.deleted, "connectors/"+tenant.String()+"/send-email/apiKey")
	assert.Zero(t, failed)
}
