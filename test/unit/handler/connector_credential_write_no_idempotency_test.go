package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func noIdemCacheKeys(c *memCache) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, 0, len(c.items))
	for k := range c.items {
		keys = append(keys, k)
	}
	return keys
}

func TestWriteConnectorCredential_WithIdempotencyKey_LeavesNothingInTheCache(t *testing.T) {
	t.Parallel()

	cache := newMemCache()
	store := &credentialWriteStore{}
	r := w11cEngine(handler.Services{
		Connectors: service.NewConnectorService(service.ConnectorDeps{Secrets: store}),
	}, cache)
	body := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "no-idem-secret"}

	w := w11cSend(r, http.MethodPost, credentialWritePath, body, "credential-key")

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Empty(t, noIdemCacheKeys(cache), "no reservation and no stored response, so no fingerprint of the secret is kept")
	assert.Len(t, store.calls(), 1)
}

func TestWriteConnectorCredential_SameKeyDifferentBodies_BothReachTheSecretsStore(t *testing.T) {
	t.Parallel()

	cache := newMemCache()
	store := &credentialWriteStore{}
	r := w11cEngine(handler.Services{
		Connectors: service.NewConnectorService(service.ConnectorDeps{Secrets: store}),
	}, cache)
	first := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "old-secret"}
	second := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "new-secret"}

	w1 := w11cSend(r, http.MethodPost, credentialWritePath, first, "shared-key")
	w2 := w11cSend(r, http.MethodPost, credentialWritePath, second, "shared-key")

	require.Equal(t, http.StatusCreated, w1.Code, w1.Body.String())
	require.Equal(t, http.StatusCreated, w2.Code, w2.Body.String())
	assert.NotContains(t, w2.Body.String(), "IDEMPOTENCY_KEY_REPLAY")
	calls := store.calls()
	require.Len(t, calls, 2)
	assert.Equal(t, "old-secret", calls[0].data["apiKey"])
	assert.Equal(t, "new-secret", calls[1].data["apiKey"])
	assert.Empty(t, noIdemCacheKeys(cache))
}

func TestWriteConnectorCredential_SameKeyAndSameBody_WritesAgainAndIsNotReplayed(t *testing.T) {
	t.Parallel()

	cache := newMemCache()
	store := &credentialWriteStore{}
	r := w11cEngine(handler.Services{
		Connectors: service.NewConnectorService(service.ConnectorDeps{Secrets: store}),
	}, cache)
	body := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "same-secret"}

	w1 := w11cSend(r, http.MethodPost, credentialWritePath, body, "repeat-key")
	w2 := w11cSend(r, http.MethodPost, credentialWritePath, body, "repeat-key")

	require.Equal(t, http.StatusCreated, w1.Code)
	require.Equal(t, http.StatusCreated, w2.Code)
	assert.Len(t, store.calls(), 2, "the repeat is a second write, not a replay of the first response")
}
