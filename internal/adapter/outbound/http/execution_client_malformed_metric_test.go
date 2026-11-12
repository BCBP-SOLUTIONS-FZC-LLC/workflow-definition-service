package http

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestExecutionClient_CheckActiveInstances_AnAnswerWithoutHasActiveCountsAsAnErrorSample(t *testing.T) {
	registerMetrics(t)
	srv := w13bNewServer(t, w13bStatus(http.StatusOK, `{"count":2}`))
	okBefore := dependencySeries(t, "execution", "check_active_instances", "success")
	errBefore := dependencySeries(t, "execution", "check_active_instances", "error")

	_, _, err := NewExecutionClient(srv.URL, 5*time.Second).
		CheckActiveInstances(context.Background(), uuid.New(), uuid.New())

	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	assert.Equal(t, okBefore, dependencySeries(t, "execution", "check_active_instances", "success"), "a call that returns an error is not a success")
	assert.Equal(t, errBefore+1, dependencySeries(t, "execution", "check_active_instances", "error"))
}
