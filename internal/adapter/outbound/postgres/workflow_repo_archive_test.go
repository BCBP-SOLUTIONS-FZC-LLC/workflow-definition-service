package postgres

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type failingRow struct{ err error }

func (r failingRow) Scan(...any) error { return r.err }

type zeroRowUpdateTx struct {
	pgx.Tx
	lookupErr error
}

func (t *zeroRowUpdateTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("UPDATE 0"), nil
}

func (t *zeroRowUpdateTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return failingRow{err: t.lookupErr}
}

func TestWorkflowRepo_UpdateActiveVersion_ZeroRowsThenLookupFails_ReturnsTheLookupError(t *testing.T) {
	t.Parallel()

	dbDown := errors.New("insufficient data left in message")
	connReset := &net.OpError{Op: "read", Err: errors.New("connection reset by peer")}

	cases := []struct {
		name            string
		lookupErr       error
		wantUnavailable bool
	}{
		{name: "database error is returned as is", lookupErr: dbDown},
		{name: "connection error is unavailable", lookupErr: connReset, wantUnavailable: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repo := NewWorkflowRepo(nil)
			ctx := withTx(context.Background(), &zeroRowUpdateTx{lookupErr: tc.lookupErr})

			err := repo.UpdateActiveVersion(ctx, uuid.New(), uuid.New(), nil)

			if !errors.Is(err, tc.lookupErr) {
				t.Fatalf("err = %v, want the lookup error", err)
			}
			for _, masked := range []error{domain.ErrNotFound, domain.ErrVersionNotPublished, domain.ErrInvalidVersionStatus} {
				if errors.Is(err, masked) {
					t.Errorf("err = %v, must not be reported as %v", err, masked)
				}
			}
			if got := errors.Is(err, domain.ErrUpstreamUnavailable); got != tc.wantUnavailable {
				t.Errorf("ErrUpstreamUnavailable = %v, want %v", got, tc.wantUnavailable)
			}
		})
	}
}
