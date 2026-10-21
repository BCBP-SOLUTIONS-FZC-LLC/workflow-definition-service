package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

var ineligible atomic.Bool

var validLevels = map[string]bool{"preparator": true, "reviewer": true, "approver": true}

func main() {
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
			Eligible bool `json:"eligible"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ineligible.Store(!body.Eligible)
		log.Printf("control: eligible=%v", body.Eligible)
		w.WriteHeader(http.StatusOK)
	})

	http.HandleFunc("GET /api/v1/internal/tenants/{tenant_id}/members/{user_id}/eligibility", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.String())

		if r.Header.Get("x-tenant-roles") != "iam-system" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		q := r.URL.Query()
		if q.Get("department_id") == "" || !validLevels[q.Get("required_level")] {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"errorCode": "VALIDATION_ERROR"})
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

	log.Printf("membership stub listening on %s (ELIGIBLE=%s)", addr, os.Getenv("ELIGIBLE"))
	srv := &http.Server{Addr: addr, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
