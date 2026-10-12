package service_test

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl/dsltest"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

//go:embed testdata/golden-library-main.bpmn
var goldenMainBPMN string

//go:embed testdata/golden-library-review.bpmn
var goldenReviewBPMN string

//go:embed testdata/golden-library-check.bpmn
var goldenCheckBPMN string

//go:embed testdata/golden-library-boundaries.bpmn
var goldenBoundariesBPMN string

//go:embed testdata/golden-flow-order.bpmn
var goldenFlowOrderBPMN string

// updateGolden rewrites workflow-models' golden from this service's publish.
// Run it with GOWORK naming a workflow-models checkout:
//
//	go test ./test/unit/service -run TestGolden -update-golden
var updateGolden = flag.Bool("update-golden", false, "rewrite workflow-models' dsltest golden from this publish")

const workflowModels = "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2"

// TestGolden publishes each golden workflow through the real compiler and
// library resolution, and requires the stored plan to be the one
// workflow-models ships to the Execution Service's tests.
func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		name, bpmnXML, file string
		golden              []byte
	}{
		{"LibraryCalls", goldenMainBPMN, dsltest.LibraryCallsFile, dsltest.LibraryCalls()},
		{"LibraryCallBoundaries", goldenBoundariesBPMN, dsltest.LibraryCallBoundariesFile, dsltest.LibraryCallBoundaries()},
		{"FlowOrder", goldenFlowOrderBPMN, dsltest.FlowOrderFile, dsltest.FlowOrder()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := publishWithLibrary(t, tc.bpmnXML, map[libraryKey]*domain.ModuleVersion{
				{"Process_Review", 0}: libraryVersion(uuid.New(), "Process_Review", 2, goldenReviewBPMN),
				{"Process_Check", 0}:  libraryVersion(uuid.New(), "Process_Check", 1, goldenCheckBPMN),
			})
			if got.err != nil {
				t.Fatalf("publish: %v", got.err)
			}
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, []byte(got.stored), "", "  "); err != nil {
				t.Fatalf("indent: %v", err)
			}
			pretty.WriteByte('\n')

			if *updateGolden {
				writeGolden(t, tc.file, pretty.Bytes())
				return
			}
			if !sameJSON(t, pretty.Bytes(), tc.golden) {
				t.Errorf("the published plan differs from workflow-models' dsltest.%s; if the change is intended, regenerate it "+
					"(go test ./test/unit/service -run TestGolden -update-golden, with GOWORK naming a workflow-models checkout) "+
					"and check the Execution Service's tests against it. Published:\n%s", tc.name, pretty.String())
			}
		})
	}
}

func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("decode published plan: %v", err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	return reflect.DeepEqual(x, y)
}

func writeGolden(t *testing.T, file string, content []byte) {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", workflowModels).Output()
	if err != nil {
		t.Fatalf("locate %s: %v", workflowModels, err)
	}
	dir := strings.TrimSpace(string(out))
	if strings.Contains(dir, string(filepath.Separator)+"pkg"+string(filepath.Separator)+"mod"+string(filepath.Separator)) {
		t.Fatalf("%s resolves to the module cache (%s); set GOWORK to a workspace that uses a workflow-models checkout", workflowModels, dir)
	}
	path := filepath.Join(dir, "pkg", "dsl", "dsltest", filepath.FromSlash(file))
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write golden: %v", err)
	}
	t.Logf("wrote %s", path)
}
