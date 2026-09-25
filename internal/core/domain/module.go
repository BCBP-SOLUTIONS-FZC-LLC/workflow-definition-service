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
	ID          uuid.UUID
	TenantID    *uuid.UUID
	Scope       CatalogScope
	Name        string
	Description string
	// ProcessID is the <bpmn:process> id every version of the module keeps; a
	// callActivity's calledElement names it.
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

// ModuleRef records one module version a published workflow version resolved
// and holds a snapshot of.
type ModuleRef struct {
	ModuleID      uuid.UUID `json:"module_id"`
	VersionID     uuid.UUID `json:"version_id"`
	ProcessID     string    `json:"process_id"`
	VersionNumber int32     `json:"version_number"`
}

// ModuleCall is a callActivity whose target is not in its own document, so it
// names a library module: a pinned version, or the active one when
// VersionNumber is zero.
type ModuleCall struct {
	CallID        string
	ProcessID     string
	VersionNumber int32
}

// ResolvedModule is one library module version to bundle, keyed by the call
// target it satisfies and compiled as the plan PlanName.
type ResolvedModule struct {
	ProcessID     string
	VersionNumber int32
	PlanName      string
	BPMNXML       string
}

// ModuleUsage is one workflow version that holds a snapshot of a module.
type ModuleUsage struct {
	WorkflowID          uuid.UUID
	WorkflowKey         string
	WorkflowName        string
	VersionID           uuid.UUID
	VersionNumber       *int32
	Status              VersionStatus
	ModuleVersionNumber int32
	// Behind reports that the module has a newer active version.
	Behind bool
}
