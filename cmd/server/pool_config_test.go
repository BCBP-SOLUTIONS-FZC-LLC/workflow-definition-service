package main

import (
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

func poolTestConfig() *config.Config {
	return &config.Config{
		DatabaseURL:         "postgres://app@pgbouncer:6432/wf",
		DatabaseFallbackURL: "postgres://app@postgres:5432/wf",
		SystemDatabaseURL:   "postgres://system@postgres:5432/wf",
		OTELServiceName:     "workflow-definition-service",
	}
}

func TestPoolConfigs_AreBuiltFromThePlatformEnvironment(t *testing.T) {
	t.Setenv("PG_MAX_CONNS", "7")
	t.Setenv("PG_MIN_CONNS", "0")
	t.Setenv("PG_BOUNCER_MODE", "true")
	t.Setenv("PG_STATEMENT_TIMEOUT", "11s")
	t.Setenv("PG_LOCK_TIMEOUT", "3s")
	t.Setenv("PG_SLOW_QUERY_THRESHOLD", "450ms")

	pools, _ := newPoolConfigs(poolTestConfig(), nopLogger{})

	if pools.app.DSN != "postgres://app@pgbouncer:6432/wf" || pools.app.MaxConns != 7 || !pools.app.PGBouncerMode {
		t.Errorf("app pool = %+v, want the application DSN, 7 connections and bouncer mode from PG_*", pools.app)
	}
	if pools.app.PoolName == pools.system.PoolName {
		t.Errorf("both pools are named %q, so platform_db_* could not tell them apart", pools.app.PoolName)
	}
	if pools.app.StatementTimeout != 11*time.Second || pools.app.LockTimeout != 3*time.Second {
		t.Errorf("app timeouts = %s and %s, want 11s and 3s from PG_STATEMENT_TIMEOUT and PG_LOCK_TIMEOUT", pools.app.StatementTimeout, pools.app.LockTimeout)
	}
	if pools.app.SlowQueryThreshold != 450*time.Millisecond {
		t.Errorf("slow query threshold = %s, want 450ms", pools.app.SlowQueryThreshold)
	}
	if pools.app.GUCProvider == nil || pools.app.Logger == nil || pools.app.Tracer == nil {
		t.Errorf("the app pool must carry the tenant GUC provider, the logger and the tracer: %+v", pools.app)
	}
}

func TestPoolConfigs_TheSystemPoolIsSmallNamedAndBypassesTheGUC(t *testing.T) {
	t.Setenv("PG_MAX_CONNS", "15")
	t.Setenv("PG_BOUNCER_MODE", "false")
	t.Setenv("PG_STATEMENT_TIMEOUT", "11s")

	pools, _ := newPoolConfigs(poolTestConfig(), nopLogger{})

	sys := pools.system
	if sys.DSN != "postgres://system@postgres:5432/wf" || sys.MaxConns != systemPoolMaxConns || sys.MinConns != 0 {
		t.Errorf("system pool = %+v, want the system DSN, %d connections and none kept idle", sys, systemPoolMaxConns)
	}
	if sys.PoolName != "system" {
		t.Errorf("system pool name = %q, want system, so platform_db_* tells the pools apart", sys.PoolName)
	}
	if !sys.PGBouncerMode {
		t.Error("the system pool must use the simple protocol whatever the app pool does")
	}
	if sys.GUCProvider != nil {
		t.Error("a BYPASSRLS system pool must not inject a tenant GUC")
	}
	if sys.StatementTimeout != 11*time.Second {
		t.Errorf("system statement timeout = %s, want the PG_STATEMENT_TIMEOUT value", sys.StatementTimeout)
	}
}

func TestPoolConfigs_TheFallbackDoesNotGoThroughTheBouncer(t *testing.T) {
	t.Setenv("PG_BOUNCER_MODE", "true")

	pools, _ := newPoolConfigs(poolTestConfig(), nopLogger{})

	if pools.fallback.DSN != "postgres://app@postgres:5432/wf" || pools.fallback.PGBouncerMode {
		t.Errorf("fallback pool = %+v, want the fallback DSN in direct mode", pools.fallback)
	}
	if pools.fallback.GUCProvider == nil {
		t.Error("the fallback pool serves the same tenant traffic, so it keeps the GUC provider")
	}
}

func TestPoolConfigs_InvalidSettingsComeBackAsWarnings(t *testing.T) {
	t.Setenv("PG_MAX_CONNS", "not-a-number")

	_, warnings := newPoolConfigs(poolTestConfig(), nopLogger{})

	if len(warnings) == 0 {
		t.Error("a malformed PG_MAX_CONNS must be reported, not silently replaced by a default")
	}
}
