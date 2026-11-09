//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func aliasDeleteSeed(t *testing.T, repo *postgres.ConnectorAliasRepo, alias string) {
	t.Helper()
	err := repo.UpsertRest(context.Background(), domain.ConnectorRestAlias{
		Alias:        alias,
		Method:       "GET",
		BaseURL:      "http://tender-service.internal",
		PathTemplate: "/x",
		Timeout:      5 * time.Second,
	})
	if err != nil {
		t.Fatalf("seed alias %q: %v", alias, err)
	}
}

func aliasDeleteStored(t *testing.T, repo *postgres.ConnectorAliasRepo, alias string) bool {
	t.Helper()
	all, err := repo.ListRest(context.Background())
	if err != nil {
		t.Fatalf("list aliases: %v", err)
	}
	for _, a := range all {
		if a.Alias == alias {
			return true
		}
	}
	return false
}

func TestConnectorAliasRepo_DeleteRest_RepeatedDelete_SecondReportsNotFound(t *testing.T) {
	cases := []struct {
		name string
	}{
		{name: "same alias deleted twice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := postgres.NewConnectorAliasRepo(newTestPool(t))
			alias := "alias-" + uuid.NewString()
			aliasDeleteSeed(t, repo, alias)

			first, firstErr := repo.DeleteRest(context.Background(), alias)
			second, secondErr := repo.DeleteRest(context.Background(), alias)

			if firstErr != nil || secondErr != nil {
				t.Fatalf("errors = %v, %v; want none", firstErr, secondErr)
			}
			if !first {
				t.Error("first delete reported not found, want deleted")
			}
			if second {
				t.Error("second delete reported deleted, want not found")
			}
			if aliasDeleteStored(t, repo, alias) {
				t.Error("alias still stored after delete")
			}
		})
	}
}

func TestConnectorAliasRepo_DeleteRest_NoExactMatch_KeepsStoredAlias(t *testing.T) {
	suffix := uuid.NewString()
	cases := []struct {
		name   string
		stored string
		asked  string
	}{
		{name: "sql characters match nothing", stored: "alias-" + suffix, asked: "' OR 1=1"},
		{name: "sql comment suffix matches nothing", stored: "alias-" + suffix, asked: "alias-" + suffix + "'--"},
		{name: "wildcard matches nothing", stored: "alias-" + suffix, asked: "alias-%"},
		{name: "alias differing only by case", stored: "alias-" + suffix, asked: "ALIAS-" + suffix},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := postgres.NewConnectorAliasRepo(newTestPool(t))
			aliasDeleteSeed(t, repo, tc.stored)

			deleted, err := repo.DeleteRest(context.Background(), tc.asked)

			if err != nil {
				t.Fatalf("DeleteRest: %v", err)
			}
			if deleted {
				t.Error("DeleteRest reported deleted, want not found")
			}
			if !aliasDeleteStored(t, repo, tc.stored) {
				t.Errorf("stored alias %q was removed by a delete of %q", tc.stored, tc.asked)
			}
			exact, err := repo.DeleteRest(context.Background(), tc.stored)
			if err != nil || !exact {
				t.Errorf("exact delete = %v, %v; want true, nil", exact, err)
			}
		})
	}
}

func TestConnectorAliasRepo_DeleteRest_DatabaseUnreachable_ReturnsErrUpstreamUnavailable(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T) (bool, error)
	}{
		{
			name: "pool closed",
			run: func(t *testing.T) (bool, error) {
				pool := newTestPool(t)
				repo := postgres.NewConnectorAliasRepo(pool)
				pool.Close()
				return repo.DeleteRest(context.Background(), "alias-"+uuid.NewString())
			},
		},
		{
			name: "delete cancelled by the statement timeout while the table is locked",
			run: func(t *testing.T) (bool, error) {
				adminPool, dsn := fixtures.NewTestPoolAndDSN(t)
				timed, err := pgcommon.NewPool(context.Background(), pgcommon.Config{
					DSN: dsn + "&options=-c%20statement_timeout%3D300ms",
				})
				if err != nil {
					t.Fatalf("open pool with statement timeout: %v", err)
				}
				t.Cleanup(timed.Close)
				repo := postgres.NewConnectorAliasRepo(timed)

				var (
					deleted bool
					delErr  error
				)
				err = adminPool.WithTx(context.Background(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
					if _, err := tx.Exec(ctx, `LOCK TABLE connector_rest_alias IN ACCESS EXCLUSIVE MODE`); err != nil {
						return err
					}
					deleted, delErr = repo.DeleteRest(ctx, "alias-"+uuid.NewString())
					return nil
				})
				if err != nil {
					t.Fatalf("hold table lock: %v", err)
				}
				return deleted, delErr
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deleted, err := tc.run(t)

			if !errors.Is(err, domain.ErrUpstreamUnavailable) {
				t.Errorf("err = %v, want ErrUpstreamUnavailable so the endpoint answers 503", err)
			}
			if errors.Is(err, domain.ErrNotFound) {
				t.Errorf("an unreachable database must not be reported as not found: %v", err)
			}
			if deleted {
				t.Error("deleted = true on failure")
			}
		})
	}
}
