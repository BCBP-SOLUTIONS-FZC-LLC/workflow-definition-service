package domain

import (
	"time"

	"github.com/google/uuid"
)

type VersionStatus string

const (
	VersionStatusDraft     VersionStatus = "DRAFT"
	VersionStatusPublished VersionStatus = "PUBLISHED"
	VersionStatusArchived  VersionStatus = "ARCHIVED"
)

type Workflow struct {
	ID                  uuid.UUID
	TenantID            uuid.UUID
	CreatedByUserID     uuid.UUID
	BusinessKey         string
	Name                string
	Description         string
	ActiveVersionID     *uuid.UUID
	ActiveVersionNumber *int32
	HasDraft            bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type WorkflowVersion struct {
	ID                   uuid.UUID
	WorkflowID           uuid.UUID
	TenantID             uuid.UUID
	Status               VersionStatus
	BPMNXML              string
	CompiledPlanJSON     *string
	ArtifactHash         string
	VersionNumber        *int32
	PublishedAt          *time.Time
	CreatedByUserID      uuid.UUID
	IsValid              bool
	ValidationErrorsJSON *string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	// RecordVersion is the optimistic-lock token bumped by the DB trigger on
	// every real update; supplied by clients on draft update.
	RecordVersion  int64
	ModuleBPMNXMLs []string // one entry per called-process BPMN; empty for most workflows
}

type NodeAssignee struct {
	ID                uuid.UUID
	TenantID          uuid.UUID
	WorkflowVersionID uuid.UUID
	NodeKey           string
	UserID            uuid.UUID
	DepartmentID      uuid.UUID
	Role              string
	CreatedAt         time.Time
}
