package main

import (
	"strings"
	"testing"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
)

var (
	appRole       = pgadapter.RolePosture{Role: "workflow_definition_app", CanWrite: true}
	systemRole    = pgadapter.RolePosture{Role: "workflow_definition_migrator", BypassRLS: true, CanWrite: true}
	superuserRole = pgadapter.RolePosture{Role: "postgres", Superuser: true, BypassRLS: true, CanWrite: true}
	readOnlyRole  = pgadapter.RolePosture{Role: "reporting", BypassRLS: true}
)

func TestCheckPoolRoles(t *testing.T) {
	for _, tc := range []struct {
		name       string
		app, sys   pgadapter.RolePosture
		appEnv     string
		configured bool
		wantErr    string
		wantWarn   bool
	}{
		{"production posture", appRole, systemRole, "prod", true, "", false},
		{"an app role that bypasses RLS is refused in dev too", superuserRole, systemRole, "dev", true, "bypasses row-level security", false},
		{"a BYPASSRLS app role is refused", pgadapter.RolePosture{Role: "app", BypassRLS: true, CanWrite: true}, systemRole, "prod", true, "bypasses row-level security", false},
		{"a system role without BYPASSRLS is refused in prod", appRole, appRole, "prod", true, "cannot bypass row-level security", false},
		{"a system role without BYPASSRLS is refused when SYSTEM_DATABASE_URL is set, even in dev", appRole, appRole, "dev", true, "cannot bypass row-level security", false},
		{"dev without SYSTEM_DATABASE_URL only warns", appRole, appRole, "dev", false, "", true},
		{"a read-only system role is refused in staging", appRole, readOnlyRole, "staging", true, "cannot write", false},
		{"a read-only system role in dev without SYSTEM_DATABASE_URL only warns", appRole, readOnlyRole, "local", false, "", true},
		{"a superuser system role is accepted", appRole, superuserRole, "prod", true, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warnings, err := checkPoolRoles(tc.app, tc.sys, tc.appEnv, tc.configured)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
			if tc.wantWarn != (len(warnings) > 0) {
				t.Errorf("warnings = %v, want a warning: %v", warnings, tc.wantWarn)
			}
		})
	}
}
