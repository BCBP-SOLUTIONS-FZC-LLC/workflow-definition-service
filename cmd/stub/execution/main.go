// Stub gRPC server for the Execution Service.
// Used for local manual testing of the definition service without a real Execution Service.
//
// Env vars:
//
//	STUB_ADDR      — listen address (default: :9091)
//	HAS_ACTIVE     — "true" to start with active instances; omit or "false" for none
//
// Runtime control (no restart needed):
//
//	POST /control  {"has_active": true|false}   toggle active-instances response
//	(HTTP on STUB_CTRL_ADDR, default :9092)
//
// Example:
//
//	go run ./cmd/stub/execution
//	curl -s -X POST localhost:9092/control -d '{"has_active":true}'
package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"sync/atomic"

	"google.golang.org/grpc"

	executionv1 "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/gen/proto/execution/v1"
)

var active atomic.Bool

type server struct {
	executionv1.UnimplementedExecutionServiceServer
}

func (s *server) CheckActiveInstances(
	_ context.Context,
	req *executionv1.CheckActiveInstancesRequest,
) (*executionv1.CheckActiveInstancesResponse, error) {
	hasActive := active.Load()
	var count int32
	if hasActive {
		count = 1
	}
	log.Printf("CheckActiveInstances tenant=%s workflow=%s → has_active=%v count=%d",
		req.TenantId, req.WorkflowId, hasActive, count)
	return &executionv1.CheckActiveInstancesResponse{HasActive: hasActive, Count: count}, nil
}

func (s *server) PauseUserTasks(
	_ context.Context,
	req *executionv1.PauseUserTasksRequest,
) (*executionv1.PauseUserTasksResponse, error) {
	log.Printf("PauseUserTasks tenant=%s user=%s → ok", req.TenantId, req.UserId)
	return &executionv1.PauseUserTasksResponse{}, nil
}

func startControlPlane(ctrlAddr string) {
	http.HandleFunc("/control", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
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
	})
	go func() {
		log.Printf("execution stub control plane on %s", ctrlAddr)
		if err := http.ListenAndServe(ctrlAddr, nil); err != nil {
			log.Fatalf("ctrl serve: %v", err)
		}
	}()
}

func main() {
	if os.Getenv("HAS_ACTIVE") == "true" {
		active.Store(true)
	}

	addr := os.Getenv("STUB_ADDR")
	if addr == "" {
		addr = ":9091"
	}
	ctrlAddr := os.Getenv("STUB_CTRL_ADDR")
	if ctrlAddr == "" {
		ctrlAddr = ":9092"
	}

	startControlPlane(ctrlAddr)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	s := grpc.NewServer()
	executionv1.RegisterExecutionServiceServer(s, &server{})
	log.Printf("execution stub listening on %s (HAS_ACTIVE=%s)", addr, os.Getenv("HAS_ACTIVE"))
	if err := s.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
