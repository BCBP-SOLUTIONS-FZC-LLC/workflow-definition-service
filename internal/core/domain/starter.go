package domain

import (
	"time"

	"github.com/google/uuid"
)

type StarterTemplate struct {
	ID                      uuid.UUID
	TenantID                *uuid.UUID
	Scope                   CatalogScope
	Name                    string
	Description             string
	Category                string
	BPMNXML                 string
	SourceWorkflowVersionID *uuid.UUID
	CreatedByUserID         *uuid.UUID
	RecordVersion           int64
	CreatedAt               time.Time
	UpdatedAt               time.Time
}
