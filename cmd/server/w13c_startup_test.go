package main

import (
	"context"
	"net"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

func w13cClosedAddr(t *testing.T) string {
	t.Helper()
	return "127.0.0.1:1"
}

func TestNewCacheStore_UnreachableValkeyWarnsOnceAndEveryCallFails(t *testing.T) {
	addr := w13cClosedAddr(t)
	cfg := &config.Config{
		ValkeyAddr:         addr,
		ValkeyDialTimeout:  time.Second,
		ValkeyReadTimeout:  time.Second,
		ValkeyWriteTimeout: time.Second,
	}
	log := &recordingLogger{}

	store, client := newCacheStore(context.Background(), cfg, log)
	t.Cleanup(func() { _ = client.Close() })

	require.NotNil(t, store)
	log.mu.Lock()
	warns := append([]string(nil), log.warns...)
	log.mu.Unlock()
	require.Len(t, warns, 1, "one warning at start-up, not one per cache call")
	assert.Contains(t, warns[0], "valkey unreachable at startup")
	assert.Contains(t, warns[0], addr, "the warning carries the dial error that names the address")

	ctx := context.Background()
	calls := []struct {
		name   string
		call   func() error
		prefix string
	}{
		{"get", func() error { _, err := store.Get(ctx, "k"); return err }, "valkey get: "},
		{"set", func() error { return store.Set(ctx, "k", "v", time.Minute) }, "valkey set: "},
		{"del", func() error { return store.Del(ctx, "k") }, "valkey del: "},
		{"setnx", func() error { _, err := store.SetNX(ctx, "k", "v", time.Minute); return err }, "valkey setnx: "},
		{"del if equal", func() error { return store.DelIfEqual(ctx, "k", "v") }, "valkey del if equal: "},
		{"ping", func() error { return store.Ping(ctx) }, "valkey ping: "},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.call()

			require.Error(t, err, "a dead cache must report failure, not a miss or a success")
			assert.True(t, strings.HasPrefix(err.Error(), tc.prefix), "error = %q", err)
		})
	}
}

func TestCheckPoolRoles_EveryAppEnvAgainstASystemRoleThatCannotBypass(t *testing.T) {
	const devSuffix = " (dev, SYSTEM_DATABASE_URL unset)"
	cases := []struct {
		appEnv     string
		configured bool
		wantErr    bool
	}{
		{"dev", false, false},
		{"local", false, false},
		{"test", false, false},
		{"dev", true, true},
		{"local", true, true},
		{"test", true, true},
		{"staging", false, true},
		{"staging", true, true},
		{"prod", false, true},
		{"prod", true, true},
		{"production", false, true},
		{"", false, true},
		{"Dev", false, true},
	}
	for _, tc := range cases {
		name := tc.appEnv
		if tc.configured {
			name += "/system url set"
		} else {
			name += "/system url unset"
		}
		t.Run(name, func(t *testing.T) {
			warnings, err := checkPoolRoles(appRole, appRole, tc.appEnv, tc.configured)

			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, warnings)
				assert.Contains(t, err.Error(), appRole.Role)
				assert.Contains(t, err.Error(), "cannot bypass row-level security")
				assert.True(t, strings.HasSuffix(err.Error(), "point SYSTEM_DATABASE_URL at the BYPASSRLS role"), "error = %q", err)
				return
			}
			require.NoError(t, err)
			require.Len(t, warnings, 1)
			assert.Contains(t, warnings[0], "cannot bypass row-level security")
			assert.Contains(t, warnings[0], appRole.Role)
			assert.True(t, strings.HasSuffix(warnings[0], devSuffix), "warning = %q", warnings[0])
		})
	}
}

func TestCheckPoolRoles_ASystemRoleThatCannotBypassAndCannotWriteWarnsTwiceInDev(t *testing.T) {
	weak := pgadapter.RolePosture{Role: "reporting_ro"}

	warnings, err := checkPoolRoles(appRole, weak, "local", false)

	require.NoError(t, err)
	require.Len(t, warnings, 2)
	assert.Contains(t, warnings[0], "cannot bypass row-level security")
	assert.Contains(t, warnings[1], "cannot write the module, starter and violation-log tables")
	for _, w := range warnings {
		assert.Contains(t, w, "reporting_ro")
		assert.True(t, strings.HasSuffix(w, " (dev, SYSTEM_DATABASE_URL unset)"), "warning = %q", w)
	}
}

func TestCheckPoolRoles_ABypassingSystemRoleThatCannotWriteIsRefusedOutsideDevWithTheWriteReason(t *testing.T) {
	for _, appEnv := range []string{"staging", "prod"} {
		t.Run(appEnv, func(t *testing.T) {
			warnings, err := checkPoolRoles(appRole, readOnlyRole, appEnv, false)

			require.Error(t, err)
			assert.Nil(t, warnings)
			assert.Contains(t, err.Error(), readOnlyRole.Role)
			assert.Contains(t, err.Error(), "cannot write the module, starter and violation-log tables")
			assert.True(t, strings.HasSuffix(err.Error(), "not a read-only one"), "error = %q", err)
		})
	}
}

func w13cServeGoroutines() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return strings.Count(string(buf[:n]), "cmd/server.goSafe.func1()")
		}
		buf = make([]byte, 2*len(buf))
	}
}

func TestServe_TwoListenersFailingAtOnceReportOneReasonAndNeitherBlocks(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Close() })
	heldAddr := held.Addr().String()
	base := w13cServeGoroutines()

	healthy := &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler(), ReadHeaderTimeout: time.Second}
	healthyFatal := make(chan error, 1)
	serve("healthy", healthy, &recordingLogger{}, healthyFatal)
	require.Eventually(t, func() bool { return w13cServeGoroutines() == base+1 }, 10*time.Second, 5*time.Millisecond,
		"the goroutine count must see a serve goroutine that is still running")

	fatal := make(chan error, 1)
	first := &http.Server{Addr: heldAddr, Handler: http.NotFoundHandler()}
	second := &http.Server{Addr: heldAddr, Handler: http.NotFoundHandler()}
	serve("http", first, &recordingLogger{}, fatal)
	serve("metrics", second, &recordingLogger{}, fatal)

	require.Eventually(t, func() bool { return len(fatal) == 1 }, 10*time.Second, 5*time.Millisecond,
		"one of the two failing listeners reports its reason")
	require.Eventually(t, func() bool { return w13cServeGoroutines() == base+1 }, 10*time.Second, 5*time.Millisecond,
		"a failing listener must hand over its reason without waiting for a reader, so both goroutines end")
	require.Len(t, fatal, 1, "only one of the two simultaneous failures is reported")
	reason := <-fatal
	assert.Contains(t, reason.Error(), "address already in use")
	assert.True(t, strings.HasPrefix(reason.Error(), "http server: ") || strings.HasPrefix(reason.Error(), "metrics server: "), "reason = %q", reason)
	assert.Empty(t, fatal, "the second reason is dropped, not queued")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, healthy.Shutdown(ctx))
	require.Eventually(t, func() bool { return w13cServeGoroutines() == base }, 10*time.Second, 5*time.Millisecond)
	assert.Empty(t, healthyFatal, "a clean shutdown of the third server reports nothing")
}
