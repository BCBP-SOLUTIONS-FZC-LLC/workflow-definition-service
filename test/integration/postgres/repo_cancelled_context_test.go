//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestRepositories_ACancelledOrExpiredContext_IsUnavailableAndKeepsItsCause(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewWorkflowRepo(pool)
	pruneRepo := postgres.NewRLSViolationRepo(pool)

	cancelled := func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx, cancel
	}
	expired := func() (context.Context, context.CancelFunc) {
		return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	}
	cases := []struct {
		name  string
		ctx   func() (context.Context, context.CancelFunc)
		call  func(context.Context) error
		cause error
	}{
		{
			name: "read with a cancelled context",
			ctx:  cancelled,
			call: func(ctx context.Context) error {
				_, err := repo.GetByID(ctx, uuid.New(), uuid.New())
				return err
			},
			cause: context.Canceled,
		},
		{
			name: "read with an expired deadline",
			ctx:  expired,
			call: func(ctx context.Context) error {
				_, err := repo.GetByID(ctx, uuid.New(), uuid.New())
				return err
			},
			cause: context.DeadlineExceeded,
		},
		{
			name: "prune with a cancelled context",
			ctx:  cancelled,
			call: func(ctx context.Context) error {
				_, err := pruneRepo.PruneBefore(ctx, time.Now())
				return err
			},
			cause: context.Canceled,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := tc.ctx()
			defer cancel()

			err := tc.call(ctx)

			require.Error(t, err)
			assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.True(t, errors.Is(err, tc.cause), "the cause stays in the chain: %v", err)
			assert.NotErrorIs(t, err, domain.ErrNotFound)
		})
	}
}
