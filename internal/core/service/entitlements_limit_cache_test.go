package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fill reads n new tenants one after another and returns their ids; the clock does not move.
func (h *limitHarness) fill(t *testing.T, n int) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.New()
		_, err := h.limitOf(context.Background(), ids[i])
		require.NoError(t, err)
	}
	h.settled(t)
	return ids
}

// fillAging reads n new tenants, moving the clock a millisecond before each so that fetch times differ.
func (h *limitHarness) fillAging(t *testing.T, n int) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.New()
		h.clock.advance(time.Millisecond)
		_, err := h.limitOf(context.Background(), ids[i])
		require.NoError(t, err)
	}
	h.settled(t)
	return ids
}

func (h *limitHarness) holds(id uuid.UUID) bool {
	h.svc.mu.Lock()
	defer h.svc.mu.Unlock()
	_, ok := h.svc.entries[id]
	return ok
}

func TestEntitlementsLimitService_TheCacheNeverHoldsMoreThanTheCap(t *testing.T) {
	reader := newGatedReader(1)
	h := newHarness(t, reader)

	ids := h.fillAging(t, entitlementsCacheCap+1)

	assert.Equal(t, entitlementsCacheCap, h.entryCount(), "one entry was dropped to make room")
	assert.False(t, h.holds(ids[0]), "with nothing stale, the oldest entry is the one dropped")
	assert.True(t, h.holds(ids[len(ids)-1]))
	assert.Equal(t, 1, reader.callsFor(ids[1]), "an entry that was kept is not read again")
}

func TestEntitlementsLimitService_AFullCacheDropsEveryEntryOlderThanTheBoundFirst(t *testing.T) {
	h := newHarness(t, newGatedReader(1))
	stale := h.fillAging(t, 5)
	h.clock.advance(11 * time.Minute)
	fresh := h.fillAging(t, entitlementsCacheCap-5)
	require.Equal(t, entitlementsCacheCap, h.entryCount())

	extra := h.fillAging(t, 1)

	assert.Equal(t, entitlementsCacheCap-5+1, h.entryCount(), "all five stale entries went, not just the oldest")
	for _, id := range stale {
		assert.False(t, h.holds(id))
	}
	assert.True(t, h.holds(fresh[0]), "a fresh entry survives while stale ones can be dropped")
	assert.True(t, h.holds(extra[0]))
}

// Two entries sit exactly at the bound: the sweep must drop both, while the fallback of dropping only the oldest would drop one.
func TestEntitlementsLimitService_AnEntryExactlyAtTheBoundIsStaleForTheSweepAndOneJustInsideIsKept(t *testing.T) {
	h := newHarness(t, newGatedReader(1))
	atBound, alsoAtBound, insideBound := uuid.New(), uuid.New(), uuid.New()
	h.prime(t, atBound)
	h.prime(t, alsoAtBound)
	h.clock.advance(time.Nanosecond)
	h.prime(t, insideBound)
	h.clock.advance(testBound - time.Nanosecond)
	h.fill(t, entitlementsCacheCap-3)
	require.Equal(t, entitlementsCacheCap, h.entryCount())

	extra := h.fill(t, 1)

	assert.False(t, h.holds(atBound), "age equal to the bound is no longer servable, so it is swept")
	assert.False(t, h.holds(alsoAtBound), "every such entry is swept, not only the oldest")
	assert.True(t, h.holds(insideBound), "one nanosecond younger is still servable")
	assert.True(t, h.holds(extra[0]))
	assert.Equal(t, entitlementsCacheCap-1, h.entryCount())
}

func TestEntitlementsLimitService_AFullCacheOfFailedEntriesIsSweptOnceTheirBackoffIsOver(t *testing.T) {
	reader := newGatedReader(1)
	reader.err = errors.New("down")
	h := newHarness(t, reader)
	failed := make([]uuid.UUID, entitlementsCacheCap)
	for i := range failed {
		failed[i] = uuid.New()
		_, err := h.limitOf(context.Background(), failed[i])
		require.ErrorIs(t, err, errUpstream)
	}
	h.settled(t)
	require.Equal(t, entitlementsCacheCap, h.entryCount(), "a failed first read counts towards the cap")
	reader.set(func(r *gatedReader) { r.err = nil })
	h.clock.advance(entitlementsRetryBackoff + time.Second)

	extra := h.fill(t, 1)

	assert.Equal(t, 1, h.entryCount(), "every failed entry whose backoff is over was swept")
	assert.True(t, h.holds(extra[0]))
	for _, id := range failed[:5] {
		assert.False(t, h.holds(id))
	}
}

func TestEntitlementsLimitService_AFailedFirstReadIsRememberedOnlyForTheBackoffThenForgottenWhenTheCacheIsFull(t *testing.T) {
	reader := newGatedReader(1)
	h := newHarness(t, reader)
	failing := uuid.New()
	reader.set(func(r *gatedReader) { r.err = errors.New("down") })
	_, err := h.limitOf(context.Background(), failing)
	require.ErrorIs(t, err, errUpstream)
	reader.set(func(r *gatedReader) { r.err = nil })
	h.svc.mu.Lock()
	e := h.svc.entries[failing]
	require.NotNil(t, e)
	assert.False(t, e.has, "a failure is never kept as a limit")
	assert.Equal(t, h.clock.Now().Add(entitlementsRetryBackoff), e.retryAt)
	h.svc.mu.Unlock()

	_, err = h.limitOf(context.Background(), failing)
	require.ErrorIs(t, err, errUpstream)
	assert.Equal(t, 1, reader.callsFor(failing), "no second read inside the backoff")

	h.clock.advance(entitlementsRetryBackoff)
	others := h.fill(t, entitlementsCacheCap-1)
	require.Equal(t, entitlementsCacheCap, h.entryCount(), "the failed entry still counts towards the cap")
	h.fill(t, 1)

	assert.False(t, h.holds(failing), "past its backoff the failed entry is the first to go")
	assert.True(t, h.holds(others[0]))
	assert.Equal(t, entitlementsCacheCap, h.entryCount())
}

func TestEntitlementsLimitService_APastTheBoundFailureLogsAnErrorAndNoWarning(t *testing.T) {
	for name, tc := range map[string]struct {
		age      time.Duration
		warnings int
		errors   int
	}{
		"just inside the bound the held value is served and warned about": {testBound - time.Nanosecond, 1, 0},
		"exactly at the bound nothing is held":                            {testBound, 0, 1},
		"past the bound nothing is held":                                  {testBound + time.Minute, 0, 1},
	} {
		t.Run(name, func(t *testing.T) {
			reader := newGatedReader(1)
			h := newHarness(t, reader)
			tenant := uuid.New()
			h.prime(t, tenant)
			reader.set(func(r *gatedReader) { r.err = errors.New("down") })
			h.clock.advance(tc.age)

			_, _ = h.limitOf(context.Background(), tenant)
			h.settled(t)

			require.Eventually(t, func() bool { return len(h.log.at("warn"))+len(h.log.at("error")) >= 1 }, 5*time.Second, time.Millisecond)
			assert.Len(t, h.log.at("warn"), tc.warnings)
			assert.Len(t, h.log.at("error"), tc.errors)
			for _, line := range append(h.log.at("warn"), h.log.at("error")...) {
				assert.Equal(t, tenant.String(), line.fields["tenant_id"])
			}
		})
	}
}
