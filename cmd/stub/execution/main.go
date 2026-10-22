package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

var active atomic.Bool

func activeInstances(w http.ResponseWriter, r *http.Request) {
	hasActive := active.Load()
	var count int32
	if hasActive {
		count = 1
	}
	log.Printf("active-instances tenant=%s workflow=%s → has_active=%v count=%d",
		r.URL.Query().Get("tenant_id"), r.PathValue("workflow_id"), hasActive, count)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"has_active": hasActive, "count": count})
}

func pauseTasks(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TenantID string `json:"tenant_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	log.Printf("pause-tasks tenant=%s user=%s → ok", body.TenantID, r.PathValue("user_id"))
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
	log.Printf("control: has_active=%v", body.HasActive)
	w.WriteHeader(http.StatusOK)
}

func main() {
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

	log.Printf("execution stub listening on %s (HAS_ACTIVE=%s)", addr, os.Getenv("HAS_ACTIVE"))
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
