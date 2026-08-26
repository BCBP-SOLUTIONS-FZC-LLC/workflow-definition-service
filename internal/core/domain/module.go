package domain

import (
	"time"

	"github.com/google/uuid"
)

type CatalogScope string

const (
	ScopeGlobal CatalogScope = "global"
	ScopeTenant CatalogScope = "tenant"
)

type Module struct {
	ID              uuid.UUID
	TenantID        *uuid.UUID
	Scope           CatalogScope
	Name            string
	Description     string
	ActiveVersionID *uuid.UUID
	CreatedByUserID *uuid.UUID
	RecordVersion   int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ModuleVersion struct {
	ID                   uuid.UUID
	ModuleID             uuid.UUID
	TenantID             *uuid.UUID
	Scope                CatalogScope
	Status               VersionStatus
	BPMNXML              string
	ProcessID            string
	VersionNumber        *int32
	IsValid              bool
	ValidationErrorsJSON *string
	PublishedAt          *time.Time
	CreatedByUserID      *uuid.UUID
	RecordVersion        int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}
