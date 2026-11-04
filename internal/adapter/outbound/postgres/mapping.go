package postgres

import (
	"encoding/json"
	"fmt"
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

func fromPgtypeTimestamptzPtr(p pgtype.Timestamptz) *time.Time {
	if !p.Valid {
		return nil
	}
	t := p.Time
	return &t
}

func toPgtypeTimestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
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
		TemplateKey:     row.TemplateKey,
		Name:            row.Name,
		Description:     fromPgtypeText(row.Description),
		ActiveVersionID: fromPgtypeUUID(row.ActiveVersionID),
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

func workflowVersionFromDB(row db.WorkflowVersion) *domain.WorkflowVersion {
	return &domain.WorkflowVersion{
		ID:                   row.ID,
		WorkflowID:           row.WorkflowID,
		TenantID:             row.TenantID,
		Status:               fromVersionStatus(row.Status),
		BPMNXML:              row.BpmnXml,
		CompiledPlanJSON:     fromJSONB(row.CompiledPlanJson),
		ArtifactHash:         fromPgtypeText(row.ArtifactHash),
		VersionNumber:        fromPgtypeInt4(row.VersionNumber),
		PublishedAt:          fromPgtypeTimestamptzPtr(row.PublishedAt),
		CreatedByUserID:      row.CreatedByUserID,
		IsValid:              row.IsValid,
		ValidationErrorsJSON: fromJSONB(row.ValidationErrorsJson),
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
		RecordVersion:        row.RecordVersion,
		ModuleRefs:           moduleRefsFromDB(row.ModuleRefs),
	}
}

func workflowVersionSummaryFromDB(row db.ListVersionsByWorkflowRow) *domain.WorkflowVersion {
	return &domain.WorkflowVersion{
		ID:              row.ID,
		WorkflowID:      row.WorkflowID,
		TenantID:        row.TenantID,
		Status:          fromVersionStatus(row.Status),
		VersionNumber:   fromPgtypeInt4(row.VersionNumber),
		PublishedAt:     fromPgtypeTimestamptzPtr(row.PublishedAt),
		CreatedByUserID: row.CreatedByUserID,
		IsValid:         row.IsValid,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
		RecordVersion:   row.RecordVersion,
	}
}

func toCatalogScope(s domain.CatalogScope) db.CatalogScope {
	return db.CatalogScope(s)
}

func fromCatalogScope(s db.CatalogScope) domain.CatalogScope {
	return domain.CatalogScope(s)
}

func moduleFromDB(row db.GetModuleByIDRow) *domain.Module {
	return &domain.Module{
		ID:              row.ID,
		TenantID:        fromPgtypeUUID(row.TenantID),
		Scope:           fromCatalogScope(row.Scope),
		Name:            row.Name,
		Description:     fromPgtypeText(row.Description),
		ProcessID:       row.ProcessID,
		ActiveVersionID: fromPgtypeUUID(row.ActiveVersionID),
		CreatedByUserID: fromPgtypeUUID(row.CreatedByUserID),
		RecordVersion:   row.RecordVersion,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

func moduleVersionFromDB(row db.GetModuleVersionByIDRow) *domain.ModuleVersion {
	return &domain.ModuleVersion{
		ID:                   row.ID,
		ModuleID:             row.ModuleID,
		TenantID:             fromPgtypeUUID(row.TenantID),
		Scope:                fromCatalogScope(row.Scope),
		Status:               fromVersionStatus(row.Status),
		BPMNXML:              row.BpmnXml,
		ProcessID:            row.ProcessID,
		VersionNumber:        fromPgtypeInt4(row.VersionNumber),
		IsValid:              row.IsValid,
		ValidationErrorsJSON: fromJSONB(row.ValidationErrorsJson),
		PublishedAt:          fromPgtypeTimestamptzPtr(row.PublishedAt),
		CreatedByUserID:      fromPgtypeUUID(row.CreatedByUserID),
		RecordVersion:        row.RecordVersion,
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
	}
}

func starterFromDB(row db.GetStarterByIDRow) *domain.StarterTemplate {
	return &domain.StarterTemplate{
		ID:                      row.ID,
		TenantID:                fromPgtypeUUID(row.TenantID),
		Scope:                   fromCatalogScope(row.Scope),
		Name:                    row.Name,
		Description:             fromPgtypeText(row.Description),
		Category:                fromPgtypeText(row.Category),
		BPMNXML:                 row.BpmnXml,
		SourceWorkflowVersionID: fromPgtypeUUID(row.SourceWorkflowVersionID),
		CreatedByUserID:         fromPgtypeUUID(row.CreatedByUserID),
		RecordVersion:           row.RecordVersion,
		CreatedAt:               row.CreatedAt,
		UpdatedAt:               row.UpdatedAt,
	}
}

func assigneeFromDB(row db.WorkflowNodeAssignee) *domain.NodeAssignee {
	return &domain.NodeAssignee{
		ID:                row.ID,
		TenantID:          row.TenantID,
		WorkflowVersionID: row.WorkflowVersionID,
		NodeKey:           row.NodeKey,
		UserID:            row.UserID,
		DepartmentID:      row.DepartmentID,
		Role:              row.Role,
		CreatedAt:         row.CreatedAt,
	}
}

func moduleRefsFromDB(raw []byte) []domain.ModuleRef {
	var refs []domain.ModuleRef
	if err := json.Unmarshal(raw, &refs); err != nil {
		return nil
	}
	return refs
}

func moduleRefsToDB(refs []domain.ModuleRef) ([]byte, error) {
	if refs == nil {
		refs = []domain.ModuleRef{}
	}
	b, err := json.Marshal(refs)
	if err != nil {
		return nil, fmt.Errorf("encode module refs: %w", err)
	}
	return b, nil
}
