//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func w10aPtr[T any](v T) *T { return &v }

func w10aAddDraft(t *testing.T, pool *pgcommon.Pool, wf *domain.Workflow) {
	t.Helper()
	require.NoError(t, pgadapter.NewWorkflowVersionRepo(pool).Create(context.Background(), newDraftVersion(wf.ID, wf.TenantID)))
}

func w10aSetCreatedAt(t *testing.T, pool *pgcommon.Pool, id uuid.UUID, at time.Time) {
	t.Helper()
	require.NoError(t, pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		tag, err := conn.Exec(ctx, `UPDATE workflow SET created_at = $1 WHERE id = $2`, at, id)
		if err != nil {
			return err
		}
		require.EqualValues(t, 1, tag.RowsAffected())
		return nil
	}))
}

func TestWorkflowRepo_List_EveryFilterNarrowsTheRowsAndTheTotalTogether(t *testing.T) {
	pool := newTestPool(t)
	repo := pgadapter.NewWorkflowRepo(pool)
	ctx := context.Background()
	tenantID := uuid.New()

	target := createListedWorkflow(ctx, t, repo, tenantID, "w10a-target", "Alpha Target")
	activateListedWorkflow(ctx, t, pool, target, true)
	w10aAddDraft(t, pool, target)

	invalid := createListedWorkflow(ctx, t, repo, tenantID, "w10a-invalid", "Alpha Invalid")
	activateListedWorkflow(ctx, t, pool, invalid, false)
	w10aAddDraft(t, pool, invalid)

	noDraft := createListedWorkflow(ctx, t, repo, tenantID, "w10a-nodraft", "Alpha NoDraft")
	activateListedWorkflow(ctx, t, pool, noDraft, true)

	archived := createListedWorkflow(ctx, t, repo, tenantID, "w10a-archived", "Alpha Archived")
	activateListedWorkflow(ctx, t, pool, archived, true)
	require.NoError(t, repo.UpdateActiveVersion(ctx, tenantID, archived.ID, nil))
	w10aAddDraft(t, pool, archived)

	otherName := createListedWorkflow(ctx, t, repo, tenantID, "w10a-othername", "Beta Name")
	activateListedWorkflow(ctx, t, pool, otherName, true)
	w10aAddDraft(t, pool, otherName)

	foreignTenant := uuid.New()
	foreign := createListedWorkflow(ctx, t, repo, foreignTenant, "w10a-target", "Alpha Target")
	activateListedWorkflow(ctx, t, pool, foreign, true)
	w10aAddDraft(t, pool, foreign)

	base := port.WorkflowFilter{
		Search: w10aPtr("alpha"), TemplateKey: w10aPtr("w10a-target"),
		IsValid: w10aPtr(true), HasDraft: w10aPtr(true), Archived: w10aPtr(false),
		Page: 1, Limit: 10,
	}
	cases := []struct {
		name   string
		change func(f *port.WorkflowFilter)
		want   []string
	}{
		{"all five filters", func(f *port.WorkflowFilter) {}, []string{"w10a-target"}},
		{"without the key", func(f *port.WorkflowFilter) { f.TemplateKey = nil }, []string{"w10a-target"}},
		{"without the search", func(f *port.WorkflowFilter) { f.Search, f.TemplateKey = nil, nil }, []string{"w10a-othername", "w10a-target"}},
		{"without the validity filter", func(f *port.WorkflowFilter) { f.IsValid, f.TemplateKey = nil, nil }, []string{"w10a-invalid", "w10a-target"}},
		{"without the draft filter", func(f *port.WorkflowFilter) { f.HasDraft, f.TemplateKey = nil, nil }, []string{"w10a-nodraft", "w10a-target"}},
		{"archived instead of active, draft and search kept", func(f *port.WorkflowFilter) {
			f.Archived, f.IsValid, f.TemplateKey = w10aPtr(true), nil, nil
		}, []string{"w10a-archived"}},
		{"archived with the validity filter finds nothing, an archived workflow has no active version", func(f *port.WorkflowFilter) {
			f.Archived, f.TemplateKey = w10aPtr(true), nil
		}, nil},
		{"the key of a near miss", func(f *port.WorkflowFilter) { f.TemplateKey = w10aPtr("w10a-invalid") }, nil},
		{"a search the target does not match", func(f *port.WorkflowFilter) { f.Search = w10aPtr("gamma") }, nil},
		{"invalid instead of valid", func(f *port.WorkflowFilter) { f.IsValid = w10aPtr(false) }, nil},
		{"no draft instead of draft", func(f *port.WorkflowFilter) { f.HasDraft = w10aPtr(false) }, nil},
		{"archived instead of active with all other filters", func(f *port.WorkflowFilter) { f.Archived = w10aPtr(true) }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := base
			tc.change(&f)

			got, total, err := repo.List(ctx, tenantID, f)

			require.NoError(t, err)
			want := tc.want
			if want == nil {
				want = []string{}
			}
			assert.Equal(t, want, listedKeys(got))
			assert.EqualValues(t, len(want), total, "the total counts the filtered set")
		})
	}

	t.Run("the total is the filtered count even when the page holds fewer rows", func(t *testing.T) {
		f := base
		f.Search, f.TemplateKey, f.Limit = nil, nil, 1

		got, total, err := repo.List(ctx, tenantID, f)

		require.NoError(t, err)
		assert.Len(t, got, 1)
		assert.EqualValues(t, 2, total)
	})
}

func TestWorkflowRepo_List_PageTwoOfAFiveRowTenant_ReturnsTheThirdAndFourthNewestAndEmptyPagesKeepTheTotal(t *testing.T) {
	pool := newTestPool(t)
	repo := pgadapter.NewWorkflowRepo(pool)
	ctx := context.Background()
	tenantID := uuid.New()
	base := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)

	for _, n := range []int{3, 5, 1, 4, 2} {
		wf := createListedWorkflow(ctx, t, repo, tenantID, "w10a-p"+string(rune('0'+n)), "Paged")
		w10aSetCreatedAt(t, pool, wf.ID, base.Add(time.Duration(n)*time.Hour))
	}
	newestOfAnother := createListedWorkflow(ctx, t, repo, uuid.New(), "w10a-p-foreign", "Paged")
	w10aSetCreatedAt(t, pool, newestOfAnother.ID, base.Add(100*time.Hour))

	keysAt := func(t *testing.T, page, limit int) ([]string, int64) {
		t.Helper()
		got, total, err := repo.List(ctx, tenantID, port.WorkflowFilter{Page: page, Limit: limit})
		require.NoError(t, err)
		keys := make([]string, 0, len(got))
		for _, w := range got {
			keys = append(keys, w.TemplateKey)
		}
		return keys, total
	}

	t.Run("pages are newest first whatever the insertion order", func(t *testing.T) {
		first, total := keysAt(t, 1, 2)
		assert.Equal(t, []string{"w10a-p5", "w10a-p4"}, first)
		assert.EqualValues(t, 5, total)

		second, total := keysAt(t, 2, 2)
		assert.Equal(t, []string{"w10a-p3", "w10a-p2"}, second)
		assert.EqualValues(t, 5, total)

		last, total := keysAt(t, 3, 2)
		assert.Equal(t, []string{"w10a-p1"}, last)
		assert.EqualValues(t, 5, total)
	})

	t.Run("a page past the last returns no rows and the real total", func(t *testing.T) {
		for _, tc := range []struct {
			name        string
			page, limit int
		}{
			{"first empty page", 4, 2},
			{"offset equal to the row count", 2, 5},
			{"far beyond", 100000, 20},
			{"offset beyond the int32 range", 1 << 40, 100},
		} {
			got, total, err := repo.List(ctx, tenantID, port.WorkflowFilter{Page: tc.page, Limit: tc.limit})

			require.NoError(t, err, tc.name)
			assert.Empty(t, got, tc.name)
			assert.EqualValues(t, 5, total, tc.name)
		}
	})

	t.Run("the same pages over HTTP", func(t *testing.T) {
		router := w6cRouter(pool, nil)

		second := w6cList(t, router, tenantID, "?page=2&limit=2")
		assert.Equal(t, []any{"w10a-p3", "w10a-p2"}, w6cKeys(second.Workflows))
		assert.Equal(t, map[string]any{"total_count": float64(5), "page": float64(2), "limit": float64(2)}, second.Pagination)

		beyond := w6cList(t, router, tenantID, "?page=2&limit=5")
		assert.Contains(t, beyond.raw, `"workflows":[]`)
		assert.Equal(t, map[string]any{"total_count": float64(5), "page": float64(2), "limit": float64(5)}, beyond.Pagination)
	})
}

type w10aProblem struct {
	Type   string `json:"type"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func w10aListAs(t *testing.T, pool *pgcommon.Pool, tenantID, userID uuid.UUID) (int, w10aProblem, string) {
	t.Helper()
	w := w6cCall(w6cRouter(pool, nil), http.MethodGet, "/api/v1/workflows", tenantID, userID, false)
	var p w10aProblem
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	return w.Code, p, w.Body.String()
}

func TestWorkflowList_DatabaseFailures_AnOutageAnswers503AndAnyOtherQueryFailureAnswers500(t *testing.T) {
	pool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()

	t.Run("a closed pool", func(t *testing.T) {
		closed, err := pgcommon.NewPool(ctx, pgcommon.Config{DSN: dsn})
		require.NoError(t, err)
		closed.Close()

		got, total, err := pgadapter.NewWorkflowRepo(closed).List(ctx, uuid.New(), port.WorkflowFilter{Page: 1, Limit: 20})

		require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
		assert.Empty(t, got)
		assert.Zero(t, total)

		status, problem, body := w10aListAs(t, closed, uuid.New(), uuid.New())
		require.Equal(t, http.StatusServiceUnavailable, status, body)
		assert.Equal(t, "UPSTREAM_UNAVAILABLE", problem.Code)
		assert.Equal(t, "https://api.workflow.platform/errors/service-unavailable", problem.Type)
		assert.Equal(t, "an upstream dependency is temporarily unavailable", problem.Detail)
	})

	t.Run("a cancelled request", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		_, _, err := pgadapter.NewWorkflowRepo(pool).List(cancelled, uuid.New(), port.WorkflowFilter{Page: 1, Limit: 20})

		require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("a count query the database refuses for a reason that is not an outage", func(t *testing.T) {
		tenantID, userID := uuid.New(), uuid.New()
		callerPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
		seed := createListedWorkflow(ctx, t, pgadapter.NewWorkflowRepo(pool), tenantID, "w10a-count", "Count")
		before, total, err := pgadapter.NewWorkflowRepo(callerPool).List(ctx, tenantID, port.WorkflowFilter{Page: 1, Limit: 20})
		require.NoError(t, err, "control: the caller reads its own workflows before the privilege is withdrawn")
		require.EqualValues(t, 1, total)
		require.Equal(t, seed.ID, before[0].ID)
		status, _, body := w10aListAs(t, callerPool, tenantID, userID)
		require.Equal(t, http.StatusOK, status, body)

		var role string
		require.NoError(t, callerPool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
			return conn.QueryRow(ctx, `SELECT current_user`).Scan(&role)
		}))
		require.NoError(t, pool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
			_, err := conn.Exec(ctx, `REVOKE SELECT ON workflow_version FROM `+pgx.Identifier{role}.Sanitize())
			return err
		}))

		got, total, err := pgadapter.NewWorkflowRepo(callerPool).List(ctx, tenantID, port.WorkflowFilter{Page: 1, Limit: 20})

		require.Error(t, err)
		assert.False(t, errors.Is(err, domain.ErrUpstreamUnavailable), "a privilege error is not an outage: %v", err)
		assert.False(t, errors.Is(err, domain.ErrNotFound))
		assert.Empty(t, got)
		assert.Zero(t, total)

		status, problem, body := w10aListAs(t, callerPool, tenantID, userID)
		require.Equal(t, http.StatusInternalServerError, status, body)
		assert.Equal(t, "INTERNAL_ERROR", problem.Code)
		assert.Equal(t, "https://api.workflow.platform/errors/internal-error", problem.Type)
		assert.Equal(t, "an unexpected error occurred", problem.Detail)
		assert.NotContains(t, body, "permission denied")
		assert.NotContains(t, body, "workflow_version")
	})
}
