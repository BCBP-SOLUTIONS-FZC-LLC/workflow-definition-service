package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
)

type plan struct {
	Code                  string `json:"code"`
	WorkflowTemplateLimit *int64 `json:"workflow_template_limit"`
}

func limit(n int64) *int64 { return &n }

func main() {
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
		log.Printf("%s %s", r.Method, r.URL.Path)
		if r.Header.Get("x-tenant-roles") != "iam-system" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"plans": plans}) //nolint:errcheck
	})

	log.Printf("catalog stub listening on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
