package contract_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/contract"
)

// recorder captures what Spec reports instead of failing the outer test, so
// these tests can require a breach to be reported.
type recorder struct {
	testing.TB
	failed bool
}

func (r *recorder) Errorf(string, ...any) { r.failed = true }
func (r *recorder) Helper()               {}

func TestSpec_ReportsBreaches(t *testing.T) {
	spec := contract.Load(t, "../../api/internal-openapi.yaml")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/internal/workflow-versions/11abcc22-0000-7000-8000-000000000001/compiled-plan?tenant_id=7ca648b2-b432-4744-884c-35fd556a310c", nil)
	json := http.Header{"Content-Type": []string{"application/json"}}

	for _, tc := range []struct {
		name   string
		status int
		body   string
		breach bool
	}{
		{"a 401 with no problem body", http.StatusUnauthorized, "", true},
		{"a status the spec does not declare", http.StatusTeapot, `{}`, true},
		{"a plan sent as a string", http.StatusOK, `{"workflow_id":"8fa88cde-824c-47bc-836b-665cd42c2222","version_id":"11abcc22-0000-7000-8000-000000000001","version_number":3,"status":"PUBLISHED","is_valid":true,"compiled_plan":"{}"}`, true},
		{"a well-formed plan", http.StatusOK, `{"workflow_id":"8fa88cde-824c-47bc-836b-665cd42c2222","version_id":"11abcc22-0000-7000-8000-000000000001","version_number":3,"status":"PUBLISHED","is_valid":true,"compiled_plan":{"schema_version":1}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{TB: t}
			spec.Response(r, req, tc.status, json, []byte(tc.body))
			if r.failed != tc.breach {
				t.Errorf("reported a breach: %v, want %v", r.failed, tc.breach)
			}
		})
	}
}

// TestSpecs_AreValid holds both of this service's documents to OpenAPI 3:
// Swagger UI mounts them side by side.
func TestSpecs_AreValid(t *testing.T) {
	contract.Load(t, "../../api/internal-openapi.yaml")
	contract.Load(t, "../../docs/swagger/openapi.yaml")
}
