package postgres

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres/db"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func toPgtypeUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

func fromPgtypeUUID(p pgtype.UUID) *uuid.UUID {
	if !p.Valid {
		return nil
	}
	id := uuid.UUID(p.Bytes)
	return &id
}

func fromPgtypeText(p pgtype.Text) string {
	if !p.Valid {
		return ""
	}
	return p.String
}

func toNullableText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func fromPgtypeInt4(p pgtype.Int4) *int32 {
	if !p.Valid {
		return nil
	}
	v := p.Int32
	return &v
}

func toPgtypeInt4(v *int32) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *v, Valid: true}
}

func fromPgtypeTimestamp(p pgtype.Timestamp) time.Time {
	if !p.Valid {
		return time.Time{}
	}
	return p.Time
}

func fromPgtypeTimestampPtr(p pgtype.Timestamp) *time.Time {
	if !p.Valid {
		return nil
	}
	t := p.Time
	return &t
}

func toPgtypeTimestamp(t *time.Time) pgtype.Timestamp {
	if t == nil {
		return pgtype.Timestamp{}
	}
	return pgtype.Timestamp{Time: *t, Valid: true}
}

func coalesceStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func toNullableJSONB(s *string) json.RawMessage {
	if s == nil || *s == "" {
		return nil
	}
	return json.RawMessage(*s)
}

func fromJSONB(b json.RawMessage) *string {
	if len(b) == 0 {
		return nil
	}
	s := string(b)
	return &s
}

// mustUUID converts a NOT-NULL pgtype.UUID to uuid.UUID. Panics if the
// column is NULL, which indicates a schema violation or migration bug.
func mustUUID(p pgtype.UUID) uuid.UUID {
	if !p.Valid {
		panic("postgres: mustUUID called on NULL pgtype.UUID — schema constraint violated")
	}
	return uuid.UUID(p.Bytes)
}

func toVersionStatus(s domain.VersionStatus) db.WorkflowVersionStatus {
	return db.WorkflowVersionStatus(s)
}

func fromVersionStatus(s db.WorkflowVersionStatus) domain.VersionStatus {
	return domain.VersionStatus(s)
}

func workflowFromDB(row db.Workflow) *domain.Workflow {
	return &domain.Workflow{
		ID:              row.ID,
		TenantID:        row.TenantID,
		CreatedByUserID: row.CreatedByUserID,
		BusinessKey:     row.BusinessKey,
		Name:            row.Name,
		Description:     fromPgtypeText(row.Description),
		ActiveVersionID: fromPgtypeUUID(row.ActiveVersionID),
		CreatedAt:       fromPgtypeTimestamp(row.CreatedAt),
		UpdatedAt:       fromPgtypeTimestamp(row.UpdatedAt),
	}
}

func workflowVersionFromDB(row db.WorkflowVersion) *domain.WorkflowVersion {
	return &domain.WorkflowVersion{
		ID:                   row.ID,
		WorkflowID:           mustUUID(row.WorkflowID),
		TenantID:             row.TenantID,
		Status:               fromVersionStatus(row.Status),
		BPMNXML:              row.BpmnXml,
		CompiledPlanJSON:     fromJSONB(row.CompiledPlanJson),
		ArtifactHash:         fromPgtypeText(row.ArtifactHash),
		VersionNumber:        fromPgtypeInt4(row.VersionNumber),
		PublishedAt:          fromPgtypeTimestampPtr(row.PublishedAt),
		CreatedByUserID:      row.CreatedByUserID,
		IsValid:              row.IsValid,
		ValidationErrorsJSON: fromJSONB(row.ValidationErrorsJson),
		CreatedAt:            fromPgtypeTimestamp(row.CreatedAt),
		UpdatedAt:            fromPgtypeTimestamp(row.UpdatedAt),
		RecordVersion:        row.RecordVersion,
		ModuleBPMNXMLs:       row.ModuleBpmnXmls,
	}
}

func assigneeFromDB(row db.WorkflowNodeAssignee) *domain.NodeAssignee {
	return &domain.NodeAssignee{
		ID:                row.ID,
		TenantID:          row.TenantID,
		WorkflowVersionID: mustUUID(row.WorkflowVersionID),
		NodeKey:           row.NodeKey,
		UserID:            row.UserID,
		DepartmentID:      row.DepartmentID,
		Role:              row.Role,
		CreatedAt:         fromPgtypeTimestamp(row.CreatedAt),
	}
}
