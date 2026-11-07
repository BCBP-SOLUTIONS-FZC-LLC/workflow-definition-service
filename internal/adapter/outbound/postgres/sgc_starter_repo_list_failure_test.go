package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type sgCCountRow struct {
	total int64
	err   error
}

func (r sgCCountRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*int64)) = r.total
	return nil
}

type sgCRows struct {
	pgx.Rows
	remaining int
	scanErr   error
	iterErr   error
}

func (r *sgCRows) Close() { /* nothing to release */ }

func (r *sgCRows) Next() bool {
	if r.remaining == 0 {
		return false
	}
	r.remaining--
	return true
}

func (r *sgCRows) Scan(...any) error { return r.scanErr }

func (r *sgCRows) Err() error { return r.iterErr }

type sgCListTx struct {
	pgx.Tx
	countErr error
	queryErr error
	rows     *sgCRows
	queries  int
}

func (t *sgCListTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return sgCCountRow{total: 7, err: t.countErr}
}

func (t *sgCListTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	t.queries++
	if t.queryErr != nil {
		return nil, t.queryErr
	}
	return t.rows, nil
}

func TestStarterRepo_List_QueryFailure_ReturnsAPlainErrorAndNoPartialList(t *testing.T) {
	t.Parallel()

	countFailed := errors.New("permission denied for table workflow_template")
	queryFailed := errors.New("syntax error at or near LIMIT")
	scanFailed := errors.New("cannot scan NULL into *string")
	iterFailed := errors.New("unexpected EOF")

	cases := []struct {
		name        string
		tx          *sgCListTx
		wantErr     error
		wantMsg     string
		wantQueries int
	}{
		{
			name:        "count query fails",
			tx:          &sgCListTx{countErr: countFailed},
			wantErr:     countFailed,
			wantMsg:     "list starters count: ",
			wantQueries: 0,
		},
		{
			name:        "data query fails",
			tx:          &sgCListTx{queryErr: queryFailed},
			wantErr:     queryFailed,
			wantMsg:     "list starters: ",
			wantQueries: 1,
		},
		{
			name:        "row scan fails",
			tx:          &sgCListTx{rows: &sgCRows{remaining: 2, scanErr: scanFailed}},
			wantErr:     scanFailed,
			wantMsg:     "list starters scan: ",
			wantQueries: 1,
		},
		{
			name:        "row iteration fails",
			tx:          &sgCListTx{rows: &sgCRows{remaining: 0, iterErr: iterFailed}},
			wantErr:     iterFailed,
			wantMsg:     "list starters rows: ",
			wantQueries: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := &StarterRepo{}

			results, total, err := repo.List(
				withTx(context.Background(), tc.tx), uuid.New(), port.StarterFilter{Page: 1, Limit: 20})

			if err == nil {
				t.Fatal("expected an error")
			}
			if results != nil || total != 0 {
				t.Errorf("results=%v total=%d, want none on failure", results, total)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want it to wrap %v", err, tc.wantErr)
			}
			if errors.Is(err, domain.ErrUpstreamUnavailable) || errors.Is(err, domain.ErrNotFound) {
				t.Errorf("err = %v, want a plain error the API reports as an internal error", err)
			}
			if got := err.Error(); !strings.HasPrefix(got, tc.wantMsg) {
				t.Errorf("err = %q, want prefix %q", got, tc.wantMsg)
			}
			if tc.tx.queries != tc.wantQueries {
				t.Errorf("data queries run = %d, want %d", tc.tx.queries, tc.wantQueries)
			}
		})
	}
}
