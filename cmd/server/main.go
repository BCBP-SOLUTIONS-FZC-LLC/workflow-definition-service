package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

var version = "dev"

func main() {
	os.Exit(realMain(os.Args, os.Stderr))
}

func realMain(args []string, stderr io.Writer) int {
	if isHealthcheckInvocation(args) {
		return runHealthcheck()
	}

	cfg, err := config.Load()
	if err != nil {
		return fail(stderr, "config error: %v", err)
	}

	if len(args) > 1 && args[1] == "migrate" {
		log, err := logger.NewLogger(cfg.AppEnv)
		if err != nil {
			return fail(stderr, "logger error: %v", err)
		}
		if err := runMigrations(context.Background(), cfg.MigrationDSN(), log); err != nil {
			return fail(stderr, "migrate error: %v", err)
		}
		return 0
	}

	a, err := newApp(cfg)
	if err != nil {
		return fail(stderr, "init error: %v", err)
	}

	if err := a.run(); err != nil {
		return fail(stderr, "stopped: %v", err)
	}
	return 0
}

func fail(stderr io.Writer, format string, err error) int {
	_, _ = fmt.Fprintf(stderr, format+"\n", err)
	return 1
}
