package main

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/logger"
)

type plan struct {
	Code                  string `json:"code"`
	WorkflowTemplateLimit *int64 `json:"workflow_template_limit"`
}

func limit(n int64) *int64 { return &n }

func main() {
	lg, err := logger.NewLogger("local")
	if err != nil {
		os.Exit(1)
	}
	addr := os.Getenv("STUB_ADDR")
	if addr == "" {
		addr = ":8084"
	}
	plans := []plan{
		{Code: "starter", WorkflowTemplateLimit: limit(5)},
		{Code: "pro", WorkflowTemplateLimit: limit(50)},
		{Code: "enterprise"},
	}

	http.HandleFunc("/api/v1/internal/plans", func(w http.ResponseWriter, r *http.Request) {
		lg.Info("request", map[string]any{"method": r.Method, "path": r.URL.Path})
		if r.Header.Get("x-tenant-roles") != "iam-system" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"plans": plans})
	})

	lg.Info("catalog stub listening", map[string]any{"addr": addr})
	srv := &http.Server{Addr: addr, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		lg.Error("serve failed", map[string]any{"error": err.Error()})
		os.Exit(1)
	}
}
