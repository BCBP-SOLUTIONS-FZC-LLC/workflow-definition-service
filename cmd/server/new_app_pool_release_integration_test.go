//go:build integration

package main

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cmdSrvSessionsOf(t *testing.T, superDSN, role string) int {
	t.Helper()
	var n int
	err := cmdSrvOpenAdmin(t, superDSN).QueryRowContext(context.Background(),
		`SELECT count(*) FROM pg_stat_activity WHERE usename = $1`, role).Scan(&n)
	require.NoError(t, err)
	return n
}

func TestNewApp_TheSystemPoolCannotConnect_TheApplicationPoolIsClosedAndNoSessionIsLeft(t *testing.T) {
	superDSN := cmdSrvSuperDSN(t)
	role, roleDSN := cmdSrvAppRole(t, superDSN)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "none")
	t.Setenv("LOG_FILE", filepath.Join(t.TempDir(), "app.log"))

	healthy := cmdSrvAppConfig(roleDSN)
	healthy.SystemDatabaseURL = superDSN
	a, err := newApp(healthy)
	require.NoError(t, err)
	closeApp := sync.OnceFunc(func() { cmdSrvCloseApp(t, a) })
	t.Cleanup(closeApp)
	require.Positive(t, cmdSrvSessionsOf(t, superDSN, role),
		"control: a started service holds sessions of the application role, so the count below can tell a leak")
	closeApp()
	require.Eventually(t, func() bool { return cmdSrvSessionsOf(t, superDSN, role) == 0 }, 10*time.Second, 50*time.Millisecond,
		"control: closing the pools ends the sessions")

	failing := cmdSrvAppConfig(roleDSN)
	failing.SystemDatabaseURL = cmdSrvCovUnreachable

	got, err := newApp(failing)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "system db pool: ")
	assert.Eventually(t, func() bool { return cmdSrvSessionsOf(t, superDSN, role) == 0 }, 10*time.Second, 50*time.Millisecond,
		"the application pool opened before the system pool failed must not keep a session")
}
