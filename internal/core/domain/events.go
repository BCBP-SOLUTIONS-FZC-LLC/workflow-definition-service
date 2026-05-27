package domain

import (
	"time"

	"github.com/google/uuid"
)

type OutboxStatus string

const (
	OutboxStatusPending OutboxStatus = "PENDING"
	OutboxStatusSent    OutboxStatus = "SENT"
	OutboxStatusFailed  OutboxStatus = "FAILED"
)

type OutboxEvent struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	Topic       string
	PayloadJSON []byte
	Status      OutboxStatus
	RetryCount  int32
	RetryAfter  *time.Time
	CreatedAt   time.Time
	ProcessedAt *time.Time
}
