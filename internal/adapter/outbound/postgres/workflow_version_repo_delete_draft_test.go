package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type scriptedRow struct{ err error }

func (r scriptedRow) Scan(...any) error { return r.err }

type zeroRowDeleteTx struct {
	pgx.Tx
	lookupErr error
	lookups   int
}

func (f *zeroRowDeleteTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("DELETE 0"), nil
}

func (f *zeroRowDeleteTx) QueryRow(context.Context, string, ...any) pgx.Row {
	f.lookups++
	return scriptedRow{err: f.lookupErr}
}

func TestWorkflowVersionRepo_DeleteDraft_FollowUpLookupFails_ReturnsTheLookupError(t *testing.T) {
	t.Parallel()

	lookupFailure := errors.New("scan workflow version: unexpected column type")
	cases := []struct {
		name      string
		lookupErr error
		wantErr   error
	}{
		{name: "non-connection error", lookupErr: lookupFailure, wantErr: lookupFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tx := &zeroRowDeleteTx{lookupErr: tc.lookupErr}
			ctx := withTx(context.Background(), tx)

			err := NewWorkflowVersionRepo(nil).DeleteDraft(ctx, uuid.New(), uuid.New())

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			for _, notWanted := range []error{
				domain.ErrNotFound, domain.ErrVersionNotDraft, domain.ErrUpstreamUnavailable,
			} {
				if errors.Is(err, notWanted) {
					t.Errorf("err = %v, must not be classified as %v", err, notWanted)
				}
			}
			if tx.lookups != 1 {
				t.Errorf("version lookups = %d, want 1", tx.lookups)
			}
		})
	}
}
