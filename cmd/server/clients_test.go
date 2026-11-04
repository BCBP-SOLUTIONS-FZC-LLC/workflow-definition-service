package main

import (
	"testing"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/config"
)

// An unset address must give a nil interface, not a nil pointer wrapped in
// one: services skip a nil client, and a typed nil would pass that check and
// panic on the first call.
func TestOutboundClients_UnsetAddressIsANilInterface(t *testing.T) {
	cfg := &config.Config{ExecutionClientTimeout: time.Second, MembershipClientTimeout: time.Second}
	if svc := newExecutionService(cfg); svc != nil {
		t.Errorf("execution service = %#v, want nil", svc)
	}
	if svc := newMembershipService(cfg); svc != nil {
		t.Errorf("membership service = %#v, want nil", svc)
	}
}

func TestOutboundClients_SetAddressBuildsAClient(t *testing.T) {
	cfg := &config.Config{
		ExecutionServiceURL: "http://execution:8080", ExecutionServiceToken: "t", ExecutionClientTimeout: time.Second,
		OrgMembershipBaseURL: "http://org:8080", MembershipClientTimeout: time.Second,
	}
	if newExecutionService(cfg) == nil || newMembershipService(cfg) == nil {
		t.Error("a configured address built no client")
	}
}
