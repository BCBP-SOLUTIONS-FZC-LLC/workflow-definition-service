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
	TemplateKey         string
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
	RecordVersion        int64
	ModuleRefs           []ModuleRef
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
