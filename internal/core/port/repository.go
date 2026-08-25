package port

import (
	"context"
	"encoding/json"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type WorkflowRepository interface {
	Create(ctx context.Context, w *domain.Workflow) error
	GetByID(ctx context.Context, tenantID, id uuid.UUID) (*domain.Workflow, error)
	GetByBusinessKey(ctx context.Context, tenantID uuid.UUID, businessKey string) (*domain.Workflow, error)
	List(ctx context.Context, tenantID uuid.UUID, filter WorkflowFilter) ([]*domain.Workflow, int64, error)
	UpdateActiveVersion(ctx context.Context, tenantID, workflowID uuid.UUID, versionID *uuid.UUID) error
	UpdateMetadata(ctx context.Context, tenantID, workflowID uuid.UUID, name, description string) error
	CountByTenant(ctx context.Context, tenantID uuid.UUID) (int64, error)
}

type WorkflowVersionRepository interface {
	Create(ctx context.Context, v *domain.WorkflowVersion) error
	GetByID(ctx context.Context, tenantID, id uuid.UUID) (*domain.WorkflowVersion, error)
	GetDraft(ctx context.Context, tenantID, workflowID uuid.UUID) (*domain.WorkflowVersion, error)
	ListByWorkflow(
		ctx context.Context,
		tenantID, workflowID uuid.UUID,
		page, limit int,
	) ([]*domain.WorkflowVersion, int64, error)
	UpdateDraft(ctx context.Context, v *domain.WorkflowVersion) error
	Publish(
		ctx context.Context,
		tenantID, versionID uuid.UUID,
		versionNumber int32,
		compiledPlanJSON, artifactHash string,
	) error
	Archive(ctx context.Context, tenantID, versionID uuid.UUID) error
	DeleteDraft(ctx context.Context, tenantID, versionID uuid.UUID) error
	SetInvalid(ctx context.Context, tenantID, versionID uuid.UUID, errorsJSON string) error
	NextVersionNumber(ctx context.Context, tenantID, workflowID uuid.UUID) (int32, error)
}

type AssigneeRepository interface {
	BulkInsert(
		ctx context.Context,
		tenantID, versionID uuid.UUID,
		assignees []*domain.NodeAssignee,
	) error
	ListByUser(ctx context.Context, tenantID, userID uuid.UUID) ([]*domain.NodeAssignee, error)
	DeleteByVersion(ctx context.Context, tenantID, versionID uuid.UUID) error
}

// OutboxRepository enqueues events inside a business transaction.
// The outbox relay (fetch → publish → mark-sent/failed) is handled entirely
// by the platform-events outbox.Runner.
// The envelope is stored as canonical JSON by platform-events so the runner can
// deserialise and publish it without any service-side transformation.
type OutboxRepository interface {
	Enqueue(ctx context.Context, env events.Envelope[json.RawMessage]) error
}

type ProcessedEventRepository interface {
	// RecordIfNew inserts the (event_id, consumer) pair and returns true if it
	// was new, false if it was already processed (ON CONFLICT DO NOTHING).
	// eventType is stored for observability and may be empty.
	RecordIfNew(ctx context.Context, eventID uuid.UUID, consumer, eventType string) (bool, error)
}

type ConnectorAliasRepository interface {
	ListRest(ctx context.Context) ([]domain.ConnectorRestAlias, error)
	ListSQL(ctx context.Context) ([]domain.ConnectorSQLAlias, error)
	UpsertRest(ctx context.Context, a domain.ConnectorRestAlias) error
	UpsertSQL(ctx context.Context, q domain.ConnectorSQLAlias) error
	DeleteRest(ctx context.Context, alias string) (bool, error)
	DeleteSQL(ctx context.Context, alias string) (bool, error)
}

type WorkflowFilter struct {
	Search      *string
	BusinessKey *string
	IsValid     *bool
	HasDraft    *bool
	Archived    *bool
	Page        int
	Limit       int
}
