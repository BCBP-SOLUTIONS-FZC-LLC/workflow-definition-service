//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var starterListSeedClock atomic.Int64

func seedStarterForList(
	t *testing.T,
	pool *pgcommon.Pool,
	repo *postgres.StarterRepo,
	tenantID *uuid.UUID,
	name, category string,
) uuid.UUID {
	t.Helper()
	scope := domain.ScopeGlobal
	if tenantID != nil {
		scope = domain.ScopeTenant
	}
	id := uuid.New()
	st := &domain.StarterTemplate{
		ID: id, TenantID: tenantID, Scope: scope, Name: name, Category: category, BPMNXML: "<bpmn/>",
	}
	if err := repo.Create(context.Background(), st); err != nil {
		t.Fatalf("seed starter %q: %v", name, err)
	}
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).
		Add(time.Duration(starterListSeedClock.Add(1)) * time.Second)
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx, `UPDATE workflow_template SET created_at = $1 WHERE id = $2`, createdAt, id)
		return err
	})
	if err != nil {
		t.Fatalf("set created_at of starter %q: %v", name, err)
	}
	return id
}

func starterListIDs(results []*domain.StarterTemplate) []uuid.UUID {
	ids := make([]uuid.UUID, len(results))
	for i, s := range results {
		ids[i] = s.ID
	}
	return ids
}

func sameStarterIDs(got, want []uuid.UUID) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestStarterRepoList_ScopeFilter_ReturnsOnlyThatScopeVisibleToTheCaller(t *testing.T) {
	cases := []struct {
		name       string
		scope      domain.CatalogScope
		wantOwn    bool
		wantGlobal bool
	}{
		{"tenant scope keeps the caller's own starters only", domain.ScopeTenant, true, false},
		{"global scope keeps global starters only", domain.ScopeGlobal, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)
			repo := postgres.NewStarterRepo(pool)
			ctx := context.Background()
			tenantA, tenantB := uuid.New(), uuid.New()
			ownID := seedStarterForList(t, pool, repo, &tenantA, "own", "")
			otherID := seedStarterForList(t, pool, repo, &tenantB, "other tenant", "")
			globalID := seedStarterForList(t, pool, repo, nil, "shared", "")

			scope := tc.scope
			results, total, err := repo.List(ctx, tenantA, port.StarterFilter{Scope: &scope, Page: 1, Limit: 20})
			if err != nil {
				t.Fatalf("List: %v", err)
			}

			var want []uuid.UUID
			if tc.wantOwn {
				want = append(want, ownID)
			}
			if tc.wantGlobal {
				want = append(want, globalID)
			}
			if total != int64(len(want)) || !sameStarterIDs(starterListIDs(results), want) {
				t.Fatalf("total=%d ids=%v, want exactly %v (other tenant's starter was %v)",
					total, starterListIDs(results), want, otherID)
			}
			for _, s := range results {
				if s.Scope != tc.scope {
					t.Errorf("starter %v has scope %q, want %q", s.ID, s.Scope, tc.scope)
				}
				if tc.scope == domain.ScopeGlobal && s.TenantID != nil {
					t.Errorf("global starter %v has tenant %v, want none", s.ID, *s.TenantID)
				}
			}
		})
	}
}

func TestStarterRepoList_CombinedFilters_AreAndedAndTotalCountsTheFilteredSet(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewStarterRepo(pool)
	ctx := context.Background()
	tenantA, tenantB := uuid.New(), uuid.New()

	older := seedStarterForList(t, pool, repo, &tenantA, "ABC first", "hr")
	newer := seedStarterForList(t, pool, repo, &tenantA, "xabcx second", "hr")
	seedStarterForList(t, pool, repo, &tenantA, "abc other category", "finance")
	seedStarterForList(t, pool, repo, &tenantA, "different name", "hr")
	seedStarterForList(t, pool, repo, &tenantB, "abc other tenant", "hr")
	seedStarterForList(t, pool, repo, nil, "abc global", "hr")

	scope, category, search := domain.ScopeTenant, "hr", "abc"
	results, total, err := repo.List(ctx, tenantA, port.StarterFilter{
		Scope: &scope, Category: &category, Search: &search, Page: 1, Limit: 1,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if total != 2 {
		t.Errorf("total = %d, want 2: the count covers the filtered set, not the page size", total)
	}
	if got := starterListIDs(results); !sameStarterIDs(got, []uuid.UUID{newer}) {
		t.Errorf("page of one = %v, want the newest match %v", got, newer)
	}

	all, allTotal, err := repo.List(ctx, tenantA, port.StarterFilter{
		Scope: &scope, Category: &category, Search: &search, Page: 1, Limit: 20,
	})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if allTotal != 2 || !sameStarterIDs(starterListIDs(all), []uuid.UUID{newer, older}) {
		t.Errorf("total=%d ids=%v, want the two matching starters newest first %v",
			allTotal, starterListIDs(all), []uuid.UUID{newer, older})
	}
}

func TestStarterRepoList_InjectionStyleValues_AreBoundAsLiteralText(t *testing.T) {
	cases := []struct {
		name     string
		search   string
		category string
		wantName string
	}{
		{"quote injection in q matches only the name containing it", "' OR 1=1 --", "", "x' OR 1=1 -- y"},
		{"statement terminator in q is literal text", "'; DROP TABLE workflow_template; --", "", ""},
		{"quote injection in category matches nothing", "", "' OR '1'='1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)
			repo := postgres.NewStarterRepo(pool)
			ctx := context.Background()
			tenantID := uuid.New()
			seedStarterForList(t, pool, repo, &tenantID, "x' OR 1=1 -- y", "hr")
			seedStarterForList(t, pool, repo, &tenantID, "Plain Alpha", "hr")
			seedStarterForList(t, pool, repo, &tenantID, "Plain Beta", "finance")

			filter := port.StarterFilter{Page: 1, Limit: 20}
			if tc.search != "" {
				filter.Search = &tc.search
			}
			if tc.category != "" {
				filter.Category = &tc.category
			}
			results, total, err := repo.List(ctx, tenantID, filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}

			var wantTotal int64
			if tc.wantName != "" {
				wantTotal = 1
			}
			if total != wantTotal || int64(len(results)) != wantTotal {
				t.Fatalf("total=%d len=%d, want %d", total, len(results), wantTotal)
			}
			if tc.wantName != "" && results[0].Name != tc.wantName {
				t.Errorf("name = %q, want %q", results[0].Name, tc.wantName)
			}

			all, allTotal, err := repo.List(ctx, tenantID, port.StarterFilter{Page: 1, Limit: 20})
			if err != nil {
				t.Fatalf("List after the hostile filter: %v", err)
			}
			if allTotal != 3 || len(all) != 3 {
				t.Errorf("the tenant now has total=%d len=%d starters, want 3 (table must be intact)", allTotal, len(all))
			}
		})
	}
}

func TestStarterRepoList_Paging_OrdersNewestFirstAndKeepsTheTotal(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewStarterRepo(pool)
	ctx := context.Background()
	tenantID := uuid.New()

	const seeded = 12
	oldestFirst := make([]uuid.UUID, seeded)
	for i := range oldestFirst {
		oldestFirst[i] = seedStarterForList(t, pool, repo, &tenantID, fmt.Sprintf("starter %02d", i+1), "")
	}
	newestFirst := make([]uuid.UUID, seeded)
	for i, id := range oldestFirst {
		newestFirst[seeded-1-i] = id
	}

	cases := []struct {
		name  string
		page  int
		limit int
		want  []uuid.UUID
	}{
		{"first page", 1, 5, newestFirst[0:5]},
		{"second page holds items 6 to 10", 2, 5, newestFirst[5:10]},
		{"last page holds the remainder", 3, 5, newestFirst[10:12]},
		{"past the last page is empty", 100000, 100, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results, total, err := repo.List(ctx, tenantID, port.StarterFilter{Page: tc.page, Limit: tc.limit})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if total != seeded {
				t.Errorf("total = %d, want %d on every page", total, seeded)
			}
			if got := starterListIDs(results); !sameStarterIDs(got, tc.want) {
				t.Errorf("ids = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStarterRepoList_ClosedPool_IsUnavailableNotAnEmptyList(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewStarterRepo(pool)
	pool.Close()

	results, total, err := repo.List(context.Background(), uuid.New(), port.StarterFilter{Page: 1, Limit: 20})

	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Errorf("List on a closed pool = %v, want ErrUpstreamUnavailable so the read answers 503", err)
	}
	if len(results) != 0 || total != 0 {
		t.Errorf("a failed read returned results=%v total=%d, want nothing", results, total)
	}
}
