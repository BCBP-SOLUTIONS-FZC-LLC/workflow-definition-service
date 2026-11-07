package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type noRowThenLookupFailureTx struct {
	pgx.Tx
	lookupErr error
	queries   int
}

func (f *noRowThenLookupFailureTx) QueryRow(context.Context, string, ...any) pgx.Row {
	f.queries++
	if f.queries == 1 {
		return scriptedRow{err: pgx.ErrNoRows}
	}
	return scriptedRow{err: f.lookupErr}
}

func TestWorkflowVersionRepo_UpdateDraft_ZeroRowsAndStatusLookupFails_ReturnsTheLookupError(t *testing.T) {
	t.Parallel()

	lookupFailure := errors.New("scan workflow version: unexpected column type")
	cases := []struct {
		name      string
		lookupErr error
	}{
		{name: "non-connection error", lookupErr: lookupFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tx := &noRowThenLookupFailureTx{lookupErr: tc.lookupErr}
			ctx := withTx(context.Background(), tx)
			draft := &domain.WorkflowVersion{ID: uuid.New(), TenantID: uuid.New(), RecordVersion: 4}

			err := NewWorkflowVersionRepo(nil).UpdateDraft(ctx, draft)

			if !errors.Is(err, tc.lookupErr) {
				t.Fatalf("err = %v, want %v", err, tc.lookupErr)
			}
			for _, notWanted := range []error{
				domain.ErrNotFound, domain.ErrVersionNotDraft, domain.ErrDraftConcurrency, domain.ErrUpstreamUnavailable,
			} {
				if errors.Is(err, notWanted) {
					t.Errorf("err = %v, must not be classified as %v", err, notWanted)
				}
			}
			if tx.queries != 2 {
				t.Errorf("queries = %d, want the update then one status lookup", tx.queries)
			}
			if draft.RecordVersion != 4 {
				t.Errorf("record_version = %d, want it untouched at 4", draft.RecordVersion)
			}
		})
	}
}
