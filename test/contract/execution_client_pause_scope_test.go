package contract_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/contract"
)

func TestExecutionClient_PauseUserTasksBody(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	userID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	deptID := uuid.MustParse("018e1f2a-0000-7000-8000-000000000010")
	for _, tc := range []struct {
		name     string
		dept     *uuid.UUID
		wantBody string
	}{
		{
			name:     "a department revocation sends department_id next to tenant_id",
			dept:     &deptID,
			wantBody: `{"department_id":"018e1f2a-0000-7000-8000-000000000010","tenant_id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}`,
		},
		{
			name:     "a full membership revocation omits department_id",
			dept:     nil,
			wantBody: `{"tenant_id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := contract.Load(t, executionSpec)
			var gotBody, gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				spec.Request(t, r)
				b, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read body: %v", err)
				}
				gotBody, gotPath = string(b), r.URL.Path
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(srv.Close)

			if err := executionClient(srv.URL).PauseUserTasks(context.Background(), tenantID, userID, tc.dept); err != nil {
				t.Fatalf("PauseUserTasks: %v", err)
			}

			if want := "/api/v1/internal/users/" + userID.String() + "/pause-tasks"; gotPath != want {
				t.Errorf("path = %q, want %q", gotPath, want)
			}
			if gotBody != tc.wantBody {
				t.Errorf("body = %s, want %s", gotBody, tc.wantBody)
			}
		})
	}
}
