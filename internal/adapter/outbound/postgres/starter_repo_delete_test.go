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

type starterDeleteScriptedRow struct{ err error }

func (r starterDeleteScriptedRow) Scan(...any) error { return r.err }

type starterDeleteScriptedTx struct {
	pgx.Tx
	queryRowErr error
	execTag     string
	execErr     error
}

func (t *starterDeleteScriptedTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return starterDeleteScriptedRow{err: t.queryRowErr}
}

func (t *starterDeleteScriptedTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(t.execTag), t.execErr
}

func TestStarterRepo_Delete_StatementOutcome_MapsToDomainError(t *testing.T) {
	t.Parallel()

	connReset := &net.OpError{Op: "write", Err: errors.New("connection reset by peer")}
	plain := errors.New("insufficient data left in message")

	cases := []struct {
		name            string
		tag             string
		execErr         error
		wantErr         error
		wantUnavailable bool
	}{
		{name: "no row removed is not found", tag: "DELETE 0", wantErr: domain.ErrNotFound},
		{name: "connection error is unavailable", execErr: connReset, wantErr: connReset, wantUnavailable: true},
		{name: "other database error is returned as is", execErr: plain, wantErr: plain},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repo := NewStarterRepo(nil)
			ctx := withTx(context.Background(), &starterDeleteScriptedTx{execTag: tc.tag, execErr: tc.execErr})

			err := repo.Delete(ctx, uuid.New(), uuid.New())

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got := errors.Is(err, domain.ErrUpstreamUnavailable); got != tc.wantUnavailable {
				t.Errorf("ErrUpstreamUnavailable = %v, want %v", got, tc.wantUnavailable)
			}
			if tc.wantErr != domain.ErrNotFound && errors.Is(err, domain.ErrNotFound) {
				t.Errorf("err = %v, must not be reported as not found", err)
			}
		})
	}
}

func TestStarterRepo_Delete_RowRemoved_ReturnsNil(t *testing.T) {
	t.Parallel()

	repo := NewStarterRepo(nil)
	ctx := withTx(context.Background(), &starterDeleteScriptedTx{execTag: "DELETE 1"})

	if err := repo.Delete(ctx, uuid.New(), uuid.New()); err != nil {
		t.Fatalf("Delete = %v, want nil", err)
	}
}

func TestStarterRepo_GetByID_LookupFails_MapsToDomainError(t *testing.T) {
	t.Parallel()

	connReset := &net.OpError{Op: "read", Err: errors.New("connection reset by peer")}
	plain := errors.New("insufficient data left in message")

	cases := []struct {
		name            string
		queryRowErr     error
		wantErr         error
		wantUnavailable bool
	}{
		{name: "no rows is not found", queryRowErr: pgx.ErrNoRows, wantErr: domain.ErrNotFound},
		{name: "connection error is unavailable", queryRowErr: connReset, wantErr: connReset, wantUnavailable: true},
		{name: "other database error is returned as is", queryRowErr: plain, wantErr: plain},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repo := NewStarterRepo(nil)
			ctx := withTx(context.Background(), &starterDeleteScriptedTx{queryRowErr: tc.queryRowErr})

			got, err := repo.GetByID(ctx, uuid.New(), uuid.New())

			if got != nil {
				t.Errorf("starter = %+v, want nil", got)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if gotUnavailable := errors.Is(err, domain.ErrUpstreamUnavailable); gotUnavailable != tc.wantUnavailable {
				t.Errorf("ErrUpstreamUnavailable = %v, want %v", gotUnavailable, tc.wantUnavailable)
			}
			if tc.wantErr != domain.ErrNotFound && errors.Is(err, domain.ErrNotFound) {
				t.Errorf("err = %v, must not be reported as not found", err)
			}
		})
	}
}
