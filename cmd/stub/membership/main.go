// Stub HTTP server for the Org & Membership Service.
// Used for local manual testing of the definition service without a real IAM service.
//
// Env vars:
//
//	STUB_ADDR  — listen address (default: :8081)
//	ELIGIBLE   — "false" to start ineligible; omit or "true" for eligible
//
// Runtime control (no restart needed):
//
//	POST /control  {"eligible": true|false}   toggle eligibility response
//
// Example:
//
//	go run ./cmd/stub/membership
//	curl -s -X POST localhost:8081/control -d '{"eligible":false}'
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync/atomic"
)

// ineligible is 1 when the stub should return 409, 0 for 200 eligible.
var ineligible atomic.Int32

func main() {
	if os.Getenv("ELIGIBLE") == "false" {
		ineligible.Store(1)
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
		if body.Eligible {
			ineligible.Store(0)
		} else {
			ineligible.Store(1)
		}
		log.Printf("control: eligible=%v", body.Eligible)
		w.WriteHeader(http.StatusOK)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.String())

		if ineligible.Load() == 1 {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"eligible": true}) //nolint:errcheck
	})

	log.Printf("membership stub listening on %s (ELIGIBLE=%s)", addr, os.Getenv("ELIGIBLE"))
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
