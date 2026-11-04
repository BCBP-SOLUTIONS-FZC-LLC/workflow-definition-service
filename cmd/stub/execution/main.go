package main

import (
	"encoding/json"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/logger"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var (
	active atomic.Bool
	lg     port.Logger
)

func activeInstances(w http.ResponseWriter, r *http.Request) {
	hasActive := active.Load()
	var count int32
	if hasActive {
		count = 1
	}
	lg.Info("active-instances", map[string]any{
		"tenant_id": r.URL.Query().Get("tenant_id"), "workflow_id": r.PathValue("workflow_id"),
		"has_active": hasActive, "count": count,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"has_active": hasActive, "count": count})
}

func pauseTasks(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TenantID string `json:"tenant_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	lg.Info("pause-tasks", map[string]any{"tenant_id": body.TenantID, "user_id": r.PathValue("user_id")})
	w.WriteHeader(http.StatusNoContent)
}

func control(w http.ResponseWriter, r *http.Request) {
	var body struct {
		HasActive bool `json:"has_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	active.Store(body.HasActive)
	lg.Info("control", map[string]any{"has_active": body.HasActive})
	w.WriteHeader(http.StatusOK)
}

func main() {
	var err error
	if lg, err = logger.NewLogger("local"); err != nil {
		os.Exit(1)
	}
	if os.Getenv("HAS_ACTIVE") == "true" {
		active.Store(true)
	}
	addr := os.Getenv("STUB_ADDR")
	if addr == "" {
		addr = ":8083"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/internal/workflows/{workflow_id}/active-instances", activeInstances)
	mux.HandleFunc("POST /api/v1/internal/users/{user_id}/pause-tasks", pauseTasks)
	mux.HandleFunc("POST /control", control)

	lg.Info("execution stub listening", map[string]any{"addr": addr, "has_active": os.Getenv("HAS_ACTIVE")})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		lg.Error("serve failed", map[string]any{"error": err.Error()})
		os.Exit(1)
	}
}
