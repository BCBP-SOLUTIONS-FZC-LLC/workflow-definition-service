package main

import (
	"context"
	"fmt"
	"os"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/logger"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

var version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	// `migrate` is a dedicated subcommand (K8s init container / pre-boot job),
	// never run from the service binary's own startup — platform-pgcommon README §Migrations.
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		log, err := logger.NewLogger(cfg.AppEnv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "logger error: %v\n", err)
			os.Exit(1)
		}
		if err := runMigrations(context.Background(), cfg.MigrationDSN(), log); err != nil {
			fmt.Fprintf(os.Stderr, "migrate error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	a, err := newApp(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init error: %v\n", err)
		os.Exit(1)
	}

	a.run()
}
