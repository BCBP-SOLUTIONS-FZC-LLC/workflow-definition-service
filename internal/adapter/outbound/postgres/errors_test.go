package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func uniqueViolation(constraint string) error {
	return &pgconn.PgError{Code: "23505", ConstraintName: constraint}
}

func TestMapErr(t *testing.T) {
	sentinel := errors.New("something else")
	unknownConstraintErr := uniqueViolation("some_other_constraint")

	tests := []struct {
		name   string
		in     error
		wantIs error
	}{
		{"nil passthrough", nil, nil},
		{"no-rows becomes ErrNotFound", pgx.ErrNoRows, domain.ErrNotFound},
		{"business key constraint", uniqueViolation(constraintWorkflowBusinessKey), domain.ErrDuplicateBusinessKey},
		{"draft constraint", uniqueViolation(constraintWorkflowVersionDraft), domain.ErrDraftAlreadyExists},
		{"published constraint", uniqueViolation(constraintWorkflowVersionPublished), domain.ErrDraftConcurrency},
		{"unknown constraint passes through", unknownConstraintErr, unknownConstraintErr},
		{"unrelated error passes through", sentinel, sentinel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapErr(tt.in)
			if !errors.Is(got, tt.wantIs) {
				t.Errorf("mapErr(%v) = %v, want (errors.Is) %v", tt.in, got, tt.wantIs)
			}
		})
	}
}
