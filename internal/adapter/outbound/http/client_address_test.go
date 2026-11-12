package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestExecutionClient_ABaseURLWithATrailingSlashCallsTheSamePathsAsOneWithout(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"has_active":false,"count":0}`))
	}))
	defer srv.Close()

	_, _, err := NewExecutionClient(srv.URL+"/", time.Second).CheckActiveInstances(context.Background(), uuid.New(), uuid.New())

	require.NoError(t, err)
	assert.False(t, strings.Contains(gotPath, "//"), "the request path %q must not carry a double slash", gotPath)
	assert.True(t, strings.HasPrefix(gotPath, "/api/v1/internal/workflows/"))
}

func TestOutboundClients_AnAddressThatCannotBuildARequestIsAnUnavailableDependencyNotAnInternalError(t *testing.T) {
	const malformed = "http://[::1"
	ctx := context.Background()

	_, errMembership := NewMembershipClient(malformed, time.Second).CheckEligibility(ctx, uuid.New(), uuid.New(), "dept", "preparator")
	errWrite := NewSecretsClient(malformed, "t", "secret", time.Second).Write(ctx, "connectors/t/x", map[string]string{"v": "1"})
	errDelete := NewSecretsClient(malformed, "t", "secret", time.Second).Delete(ctx, "connectors/t/x")

	assert.ErrorIs(t, errMembership, domain.ErrUpstreamUnavailable, "membership")
	assert.ErrorIs(t, errWrite, domain.ErrUpstreamUnavailable, "secrets write")
	assert.ErrorIs(t, errDelete, domain.ErrUpstreamUnavailable, "secrets delete")
}
