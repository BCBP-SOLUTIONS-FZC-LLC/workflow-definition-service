package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestMapMissingParent_OnlyTheNamedForeignKeyIsNotFound(t *testing.T) {
	t.Parallel()

	fk := func(constraint string) error {
		return &pgconn.PgError{Code: "23503", ConstraintName: constraint}
	}
	tests := []struct {
		name         string
		err          error
		wantNotFound bool
	}{
		{name: "the named foreign key", err: fk(constraintWorkflowVersionWorkflow), wantNotFound: true},
		{name: "another foreign key", err: fk("some_other_fkey")},
		{name: "a unique violation on the same insert", err: &pgconn.PgError{Code: "23505", ConstraintName: constraintWorkflowVersionDraft}},
		{name: "an unrelated error", err: errors.New("boom")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := mapMissingParent(tc.err, constraintWorkflowVersionWorkflow)

			if gotNotFound := errors.Is(got, domain.ErrNotFound); gotNotFound != tc.wantNotFound {
				t.Errorf("mapMissingParent(%v) = %v, not found = %v, want %v", tc.err, got, gotNotFound, tc.wantNotFound)
			}
		})
	}
}

func TestMapErr_AForeignKeyViolationIsNeverMappedGlobally(t *testing.T) {
	t.Parallel()

	err := &pgconn.PgError{Code: "23503", ConstraintName: constraintWorkflowVersionWorkflow}

	if got := mapErr(err); errors.Is(got, domain.ErrNotFound) {
		t.Errorf("mapErr(foreign key) = %v, only the version insert maps it", got)
	}
}
