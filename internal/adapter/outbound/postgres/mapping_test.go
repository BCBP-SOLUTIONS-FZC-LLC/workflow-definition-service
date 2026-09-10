package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres/db"
)

// derefStr converts a *string to its value, returning "" for nil.
// Used to flatten optional-string assertions to a single equality check.
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func TestFromPgtypeUUID(t *testing.T) {
	id := uuid.New()
	tests := []struct {
		name    string
		in      pgtype.UUID
		wantNil bool
		wantVal uuid.UUID
	}{
		{"null", pgtype.UUID{}, true, uuid.UUID{}},
		{"valid", pgtype.UUID{Bytes: id, Valid: true}, false, id},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fromPgtypeUUID(tt.in)
			if tt.wantNil {
				if got != nil {
					t.Errorf("got %v, want nil", got)
				}
				return
			}
			if got == nil || *got != tt.wantVal {
				t.Errorf("got %v, want %v", got, tt.wantVal)
			}
		})
	}
}

func TestFromPgtypeText(t *testing.T) {
	tests := []struct {
		name string
		in   pgtype.Text
		want string
	}{
		{"null", pgtype.Text{}, ""},
		{"valid", pgtype.Text{String: "hello", Valid: true}, "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fromPgtypeText(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFromPgtypeInt4(t *testing.T) {
	tests := []struct {
		name    string
		in      pgtype.Int4
		wantNil bool
		wantVal int32
	}{
		{"null", pgtype.Int4{}, true, 0},
		{"valid", pgtype.Int4{Int32: 42, Valid: true}, false, 42},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fromPgtypeInt4(tt.in)
			if tt.wantNil {
				if got != nil {
					t.Errorf("got %v, want nil", got)
				}
				return
			}
			if got == nil || *got != tt.wantVal {
				t.Errorf("got %v, want %v", got, tt.wantVal)
			}
		})
	}
}

func TestFromPgtypeTimestamp(t *testing.T) {
	now := time.Now().Truncate(time.Microsecond)
	tests := []struct {
		name     string
		in       pgtype.Timestamp
		wantZero bool
		wantTime time.Time
	}{
		{"null", pgtype.Timestamp{}, true, time.Time{}},
		{"valid", pgtype.Timestamp{Time: now, Valid: true}, false, now},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fromPgtypeTimestamp(tt.in)
			if tt.wantZero {
				if !got.IsZero() {
					t.Errorf("got %v, want zero time", got)
				}
				return
			}
			if !got.Equal(tt.wantTime) {
				t.Errorf("got %v, want %v", got, tt.wantTime)
			}
		})
	}
}

func TestWorkflowFromDB(t *testing.T) {
	vID := uuid.New()
	base := db.Workflow{
		ID: uuid.New(), TenantID: uuid.New(), CreatedByUserID: uuid.New(),
		BusinessKey: "key", Name: "Name",
	}
	tests := []struct {
		name                string
		row                 db.Workflow
		wantDescription     string
		wantActiveVersionID *uuid.UUID
	}{
		{"nullable columns absent", base, "", nil},
		{
			"all columns populated",
			db.Workflow{
				ID: base.ID, TenantID: base.TenantID, CreatedByUserID: base.CreatedByUserID,
				BusinessKey:     "my-key",
				Name:            "My Workflow",
				Description:     pgtype.Text{String: "desc", Valid: true},
				ActiveVersionID: pgtype.UUID{Bytes: vID, Valid: true},
			},
			"desc", &vID,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf := workflowFromDB(tt.row)
			if wf.Description != tt.wantDescription {
				t.Errorf("Description = %q, want %q", wf.Description, tt.wantDescription)
			}
			if tt.wantActiveVersionID == nil {
				if wf.ActiveVersionID != nil {
					t.Errorf("ActiveVersionID = %v, want nil", wf.ActiveVersionID)
				}
			} else if wf.ActiveVersionID == nil || *wf.ActiveVersionID != *tt.wantActiveVersionID {
				t.Errorf("ActiveVersionID = %v, want %v", wf.ActiveVersionID, tt.wantActiveVersionID)
			}
		})
	}
}

func TestWorkflowVersionFromDB(t *testing.T) {
	wfID := uuid.New()
	plan := `{"name":"test"}`
	errs := `[{"code":"CYCLE_DETECTED"}]`
	tests := []struct {
		name                 string
		row                  db.WorkflowVersion
		wantCompiledPlanJSON string // "" means expect nil pointer
		wantValidationErrors string // "" means expect nil pointer
	}{
		{
			"null JSONB columns",
			db.WorkflowVersion{
				ID: uuid.New(), WorkflowID: pgtype.UUID{Bytes: wfID, Valid: true},
				TenantID: uuid.New(), Status: db.WorkflowVersionStatusDRAFT,
				BpmnXml: "<bpmn/>", CreatedByUserID: uuid.New(), IsValid: true,
			},
			"", "",
		},
		{
			"populated JSONB columns",
			db.WorkflowVersion{
				ID: uuid.New(), WorkflowID: pgtype.UUID{Bytes: wfID, Valid: true},
				TenantID: uuid.New(), Status: db.WorkflowVersionStatusPUBLISHED,
				BpmnXml: "<bpmn/>", CreatedByUserID: uuid.New(), IsValid: false,
				CompiledPlanJson:     []byte(plan),
				ValidationErrorsJson: []byte(errs),
			},
			plan, errs,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := workflowVersionFromDB(tt.row)
			if got := derefStr(v.CompiledPlanJSON); got != tt.wantCompiledPlanJSON {
				t.Errorf("CompiledPlanJSON = %q, want %q", got, tt.wantCompiledPlanJSON)
			}
			if got := derefStr(v.ValidationErrorsJSON); got != tt.wantValidationErrors {
				t.Errorf("ValidationErrorsJSON = %q, want %q", got, tt.wantValidationErrors)
			}
		})
	}
}

func TestAssigneeFromDB(t *testing.T) {
	vID := uuid.New()
	deptID := uuid.New()
	row := db.WorkflowNodeAssignee{
		ID:                uuid.New(),
		TenantID:          uuid.New(),
		WorkflowVersionID: pgtype.UUID{Bytes: vID, Valid: true},
		NodeKey:           "task-1",
		UserID:            uuid.New(),
		DepartmentID:      deptID,
		Role:              "reviewer",
	}
	a := assigneeFromDB(row)
	for _, tt := range []struct {
		name string
		got  string
		want string
	}{
		{"NodeKey", a.NodeKey, "task-1"},
		{"DepartmentID", a.DepartmentID.String(), deptID.String()},
		{"Role", a.Role, "reviewer"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
	if a.WorkflowVersionID != vID {
		t.Errorf("WorkflowVersionID = %v, want %v", a.WorkflowVersionID, vID)
	}
}

func TestMustUUID_Valid(t *testing.T) {
	id := uuid.New()
	if got := mustUUID(pgtype.UUID{Bytes: id, Valid: true}); got != id {
		t.Errorf("got %v, want %v", got, id)
	}
}

func TestMustUUID_InvalidPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("mustUUID did not panic on invalid pgtype.UUID")
		}
	}()
	mustUUID(pgtype.UUID{})
}
