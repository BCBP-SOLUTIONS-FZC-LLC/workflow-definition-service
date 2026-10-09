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
	ProcessID       string
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

type ModuleRef struct {
	ModuleID      uuid.UUID `json:"module_id"`
	VersionID     uuid.UUID `json:"version_id"`
	ProcessID     string    `json:"process_id"`
	VersionNumber int32     `json:"version_number"`
}

type ModuleCall struct {
	CallID        string
	ProcessID     string
	VersionNumber int32
}

type ResolvedModule struct {
	ProcessID     string
	VersionNumber int32
	PlanName      string
	BPMNXML       string
}

type ModuleUsage struct {
	WorkflowID          uuid.UUID
	WorkflowKey         string
	WorkflowName        string
	VersionID           uuid.UUID
	VersionNumber       *int32
	Status              VersionStatus
	ModuleVersionNumber int32
	Behind              bool
}

type ModuleDeclarations struct {
	Messages   []string
	ErrorCodes []string
}
