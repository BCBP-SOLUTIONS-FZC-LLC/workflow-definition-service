package main

import (
	"encoding/json"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"
	"github.com/google/uuid"
)

var (
	ineligible  atomic.Bool
	unavailable atomic.Bool
)

var validLevels = map[string]bool{"preparator": true, "reviewer": true, "approver": true}

func main() {
	lg, err := logger.NewLogger("local")
	if err != nil {
		os.Exit(1)
	}
	if os.Getenv("ELIGIBLE") == "false" {
		ineligible.Store(true)
	}

	addr := os.Getenv("STUB_ADDR")
	if addr == "" {
		addr = ":8081"
	}

	http.HandleFunc("/control", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Eligible    bool `json:"eligible"`
			Unavailable bool `json:"unavailable"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ineligible.Store(!body.Eligible)
		unavailable.Store(body.Unavailable)
		lg.Info("control", map[string]any{"eligible": body.Eligible, "unavailable": body.Unavailable})
		w.WriteHeader(http.StatusOK)
	})

	http.HandleFunc("GET /api/v1/internal/tenants/{tenant_id}/members/{user_id}/eligibility", func(w http.ResponseWriter, r *http.Request) {
		lg.Info("request", map[string]any{"method": r.Method, "url": r.URL.String()})

		if r.Header.Get("x-tenant-roles") != "iam-system" {
			writeProblem(w, http.StatusForbidden, "FORBIDDEN")
			return
		}
		q := r.URL.Query()
		if !isUUID(r.PathValue("tenant_id")) || !isUUID(r.PathValue("user_id")) || !isUUID(q.Get("department_id")) {
			writeProblem(w, http.StatusBadRequest, "invalid_uuid")
			return
		}
		if !validLevels[q.Get("required_level")] {
			writeProblem(w, http.StatusBadRequest, "invalid_role_level")
			return
		}
		if r.Header.Get("x-tenant-id") != r.PathValue("tenant_id") {
			writeProblem(w, http.StatusBadRequest, "TENANT_MISMATCH")
			return
		}
		if unavailable.Load() {
			writeProblem(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE")
			return
		}

		answer := map[string]any{
			"tenant_id":      r.PathValue("tenant_id"),
			"user_id":        r.PathValue("user_id"),
			"department_id":  q.Get("department_id"),
			"required_level": q.Get("required_level"),
			"eligible":       !ineligible.Load(),
		}
		if ineligible.Load() {
			answer["reason"] = "not_in_department"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer)
	})

	lg.Info("membership stub listening", map[string]any{"addr": addr, "eligible": os.Getenv("ELIGIBLE")})
	srv := &http.Server{Addr: addr, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		lg.Error("serve failed", map[string]any{"error": err.Error()})
		os.Exit(1)
	}
}

func isUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

func writeProblem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "errorCode": code})
}
