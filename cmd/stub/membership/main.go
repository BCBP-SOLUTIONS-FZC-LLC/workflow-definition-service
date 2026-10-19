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

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.String())

		if ineligible.Load() {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"eligible": true})
	})

	log.Printf("membership stub listening on %s (ELIGIBLE=%s)", addr, os.Getenv("ELIGIBLE"))
	srv := &http.Server{Addr: addr, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
