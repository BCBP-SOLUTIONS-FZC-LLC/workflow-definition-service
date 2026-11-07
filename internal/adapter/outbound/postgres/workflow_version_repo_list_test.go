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

type countRow struct {
	total int64
	err   error
}

func (r countRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*int64)) = r.total
	return nil
}

type noRows struct{ pgx.Rows }

func (noRows) Close()     { /* nothing to release */ }
func (noRows) Next() bool { return false }
func (noRows) Err() error { return nil }

type versionListTx struct {
	pgx.Tx
	count    countRow
	listErr  error
	listArgs []any
}

func (t *versionListTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return t.count
}

func (t *versionListTx) Query(_ context.Context, _ string, args ...any) (pgx.Rows, error) {
	t.listArgs = args
	if t.listErr != nil {
		return nil, t.listErr
	}
	return noRows{}, nil
}

func TestWorkflowVersionRepo_ListByWorkflow_OutOfRangePaging_UsesDefaults(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		page       int
		limit      int
		wantLimit  int32
		wantOffset int32
	}{
		{"page and limit zero", 0, 0, 20, 0},
		{"page and limit negative", -4, -7, 20, 0},
		{"page zero keeps explicit limit", 0, 5, 5, 0},
		{"limit zero keeps explicit page", 3, 0, 20, 40},
		{"valid page and limit", 2, 10, 10, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tx := &versionListTx{count: countRow{total: 0}}
			repo := &WorkflowVersionRepo{}

			tenantID, workflowID := uuid.New(), uuid.New()

			_, _, err := repo.ListByWorkflow(
				withTx(context.Background(), tx), tenantID, workflowID, tc.page, tc.limit)

			if err != nil {
				t.Fatalf("ListByWorkflow: %v", err)
			}
			if len(tx.listArgs) != 4 {
				t.Fatalf("list query args = %v, want 4", tx.listArgs)
			}
			if got := tx.listArgs[0]; got != tenantID {
				t.Errorf("tenant arg = %v, want %v", got, tenantID)
			}
			if got := tx.listArgs[1]; got != workflowID {
				t.Errorf("workflow arg = %v, want %v", got, workflowID)
			}
			if got := tx.listArgs[2]; got != tc.wantLimit {
				t.Errorf("limit = %v, want %d", got, tc.wantLimit)
			}
			if got := tx.listArgs[3]; got != tc.wantOffset {
				t.Errorf("offset = %v, want %d", got, tc.wantOffset)
			}
		})
	}
}

func TestWorkflowVersionRepo_ListByWorkflow_QueryFailure_MapsTheError(t *testing.T) {
	t.Parallel()

	connectionLost := &pgconn.PgError{Code: "08006"}
	unexpected := errors.New("syntax error at or near LIMIT")

	cases := []struct {
		name            string
		countErr        error
		listErr         error
		wantUnavailable bool
		wantListReached bool
	}{
		{"count query connection error", connectionLost, nil, true, false},
		{"count query unexpected error", unexpected, nil, false, false},
		{"list query connection error", nil, connectionLost, true, true},
		{"list query unexpected error", nil, unexpected, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tx := &versionListTx{count: countRow{err: tc.countErr}, listErr: tc.listErr}
			repo := &WorkflowVersionRepo{}

			results, total, err := repo.ListByWorkflow(
				withTx(context.Background(), tx), uuid.New(), uuid.New(), 1, 20)

			if err == nil {
				t.Fatal("expected an error")
			}
			if results != nil || total != 0 {
				t.Errorf("results=%v total=%d, want none on failure", results, total)
			}
			if got := errors.Is(err, domain.ErrUpstreamUnavailable); got != tc.wantUnavailable {
				t.Errorf("errors.Is(ErrUpstreamUnavailable) = %v, want %v (err: %v)",
					got, tc.wantUnavailable, err)
			}
			if reached := tx.listArgs != nil; reached != tc.wantListReached {
				t.Errorf("list query reached = %v, want %v", reached, tc.wantListReached)
			}
			if !tc.wantUnavailable && !errors.Is(err, unexpected) {
				t.Errorf("err = %v, want the unexpected error passed through", err)
			}
		})
	}
}
