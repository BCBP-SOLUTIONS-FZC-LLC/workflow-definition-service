package postgres_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
)

func TestOutboxRepo_Enqueue(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewOutboxRepo(pool)
	transactor := postgres.NewTransactor(pool)
	ctx := context.Background()

	env := events.NewEnvelope[json.RawMessage](
		"TemplatePublished",
		"workflow-definition-service",
		json.RawMessage(`{"workflow_id":"abc"}`),
		events.WithTenantID("tenant-123"),
	)

	if err := transactor.RunInTx(ctx, func(ctx context.Context) error {
		return repo.Enqueue(ctx, env)
	}); err != nil {
		t.Fatalf("Enqueue inside tx: %v", err)
	}
}

func TestOutboxRepo_Enqueue_RequiresTx(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewOutboxRepo(pool)
	ctx := context.Background()

	env := events.NewEnvelope[json.RawMessage](
		"TemplateArchived",
		"workflow-definition-service",
		json.RawMessage(`{}`),
	)

	err := repo.Enqueue(ctx, env)
	if err == nil {
		t.Fatal("expected error when Enqueue called outside a transaction")
	}
}
