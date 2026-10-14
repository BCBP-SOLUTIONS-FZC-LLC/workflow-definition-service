package postgres

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

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
		{"tenant module process id constraint", uniqueViolation(constraintModuleProcessIDTenant), domain.ErrModuleProcessIDTaken},
		{"global module process id constraint", uniqueViolation(constraintModuleProcessIDGlobal), domain.ErrModuleProcessIDTaken},
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

func TestMapErr_AnOutageIsUnavailableNotABug(t *testing.T) {
	outages := map[string]error{
		"connection failure (08006)":   &pgconn.PgError{Code: "08006"},
		"connection does not exist":    &pgconn.PgError{Code: "08003"},
		"too many connections (53300)": &pgconn.PgError{Code: "53300"},
		"out of memory (53200)":        &pgconn.PgError{Code: "53200"},
		"admin shutdown (57P01)":       &pgconn.PgError{Code: "57P01"},
		"cannot connect now (57P03)":   &pgconn.PgError{Code: "57P03"},
		"statement timeout (57014)":    &pgconn.PgError{Code: "57014"},
		"dial failure":                 refusedDial(t),
		"network error":                &net.OpError{Op: "read", Err: errors.New("connection reset by peer")},
	}
	for name, in := range outages {
		t.Run(name, func(t *testing.T) {
			got := mapErr(in)
			if !errors.Is(got, domain.ErrUpstreamUnavailable) {
				t.Errorf("mapErr(%v) = %v, want ErrUpstreamUnavailable so the API answers 503, not 500", in, got)
			}
			if !errors.Is(got, in) {
				t.Errorf("mapErr(%v) lost its cause: %v", in, got)
			}
		})
	}

	for name, in := range map[string]error{
		"undefined table (42P01)": &pgconn.PgError{Code: "42P01"},
		"foreign key (23503)":     &pgconn.PgError{Code: "23503"},
		"deadlock (40P01)":        &pgconn.PgError{Code: "40P01"},
	} {
		t.Run("not an outage: "+name, func(t *testing.T) {
			if got := mapErr(in); errors.Is(got, domain.ErrUpstreamUnavailable) {
				t.Errorf("mapErr(%v) = %v, a client or code error must not look like an outage", in, got)
			}
		})
	}
}

func refusedDial(t *testing.T) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := pgx.Connect(ctx, "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=2")
	var dialErr *pgconn.ConnectError
	if !errors.As(err, &dialErr) {
		t.Fatalf("a refused dial = %v, want a *pgconn.ConnectError", err)
	}
	return err
}
