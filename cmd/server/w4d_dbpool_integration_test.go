//go:build integration

package main

import (
	"context"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
)

func w4dUnreachableDSN(t *testing.T) string {
	t.Helper()
	return "postgres://primary_user:w4d-secret-pw@127.0.0.1:1/wf?sslmode=disable&connect_timeout=2"
}

func w4dCurrentDatabase(t *testing.T, pool *pgcommon.Pool) string {
	t.Helper()
	var name string
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT current_database()`).Scan(&name)
	})
	require.NoError(t, err)
	return name
}

func TestNewDBPool_PrimaryUnreachable_BuildsThePoolFromTheFallback(t *testing.T) {
	superDSN := cmdSrvSuperDSN(t)
	fallbackDSN := cmdSrvEmptyDatabase(t, superDSN)
	reachableDSN := cmdSrvEmptyDatabase(t, superDSN)
	fallbackName := w4dDBName(t, fallbackDSN)
	reachableName := w4dDBName(t, reachableDSN)
	require.NotEqual(t, fallbackName, reachableName)

	cases := []struct {
		name        string
		primaryDSN  string
		hasFallback bool
		wantDB      string
		wantErr     string
	}{
		{name: "an unreachable primary with a fallback uses the fallback database", primaryDSN: w4dUnreachableDSN(t), hasFallback: true, wantDB: fallbackName},
		{name: "a reachable primary is used and the fallback is left alone", primaryDSN: reachableDSN, hasFallback: true, wantDB: reachableName},
		{name: "an unreachable primary without a fallback fails startup", primaryDSN: w4dUnreachableDSN(t), hasFallback: false, wantErr: "db pool: "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pools := poolConfigs{
				app:      pgcommon.Config{DSN: tc.primaryDSN},
				fallback: pgcommon.Config{DSN: fallbackDSN},
			}

			pool, err := newDBPool(context.Background(), pools, tc.hasFallback)

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Nil(t, pool)
				assert.Contains(t, err.Error(), tc.wantErr)
				assert.NotContains(t, err.Error(), "w4d-secret-pw")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, pool)
			t.Cleanup(pool.Close)
			assert.Equal(t, tc.wantDB, w4dCurrentDatabase(t, pool))
		})
	}
}

func w4dDBName(t *testing.T, dsn string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	return u.Path[1:]
}
