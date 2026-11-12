package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every outbound client forwards a system identity or a vault token, so a
// redirect must be returned as the answer, never followed.
func TestClients_ARedirectIsTheAnswerAndTheTargetIsNeverCalled(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(target.Close)
	var redirectsServed atomic.Int32
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectsServed.Add(1)
		http.Redirect(w, r, target.URL+"/stolen", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirector.Close)

	ctx := context.Background()
	tenant, user, workflow := uuid.New(), uuid.New(), uuid.New()
	calls := map[string]func() error{
		"openbao write": func() error {
			return NewSecretsClient(redirector.URL, "s.vault-token", "secret", time.Second).Write(ctx, "connectors/a/b", map[string]string{"k": "plaintext-credential"})
		},
		"openbao delete": func() error {
			return NewSecretsClient(redirector.URL, "s.vault-token", "secret", time.Second).Delete(ctx, "connectors/a/b")
		},
		"membership": func() error {
			_, err := NewMembershipClient(redirector.URL, time.Second).CheckEligibility(ctx, tenant, user, "dept", "role")
			return err
		},
		"catalog": func() error {
			_, err := NewCatalogClient(redirector.URL, time.Second).ReadPlanTable(ctx)
			return err
		},
		"execution check": func() error {
			_, _, err := NewExecutionClient(redirector.URL, time.Second).CheckActiveInstances(ctx, tenant, workflow)
			return err
		},
		"execution pause": func() error {
			return NewExecutionClient(redirector.URL, time.Second).PauseUserTasks(ctx, tenant, user, nil)
		},
		"entitlements": func() error {
			_, err := NewEntitlementsClient(redirector.URL, time.Second).ReadWorkflowTemplateLimit(ctx, tenant)
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			servedBefore := redirectsServed.Load()

			err := call()

			require.Error(t, err, "a 307 is not a successful answer")
			assert.Equal(t, servedBefore+1, redirectsServed.Load(), "the first server was asked once")
			assert.Zero(t, targetHits.Load(), "the redirect target must never be called")
		})
	}
}
