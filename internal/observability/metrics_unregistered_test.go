package observability_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

// withPurgeCollectorUnset swaps only the package variable for the duration of
// the test and puts the registered collector back afterwards. The registry is
// never touched. Callers must stay serial.
func withPurgeCollectorUnset(t *testing.T) *prometheus.CounterVec {
	t.Helper()
	initIdentity(t)
	registered := observability.TenantPurgeRowsDeletedTotal
	require.NotNil(t, registered)
	observability.TenantPurgeRowsDeletedTotal = nil
	t.Cleanup(func() { observability.TenantPurgeRowsDeletedTotal = registered })
	return registered
}

func TestAddTenantPurgeRows_BeforeRegister_IsANoop(t *testing.T) {
	withPurgeCollectorUnset(t)

	require.NotPanics(t, func() { observability.AddTenantPurgeRows("workflow", 5) })

	require.Nil(t, observability.TenantPurgeRowsDeletedTotal, "the call must not create a collector")
}

func TestInitTenantPurgeTables_BeforeRegister_CreatesNoSeries(t *testing.T) {
	withPurgeCollectorUnset(t)

	require.NotPanics(t, func() { observability.InitTenantPurgeTables([]string{"unregistered_purge_table"}) })

	require.Nil(t, observability.TenantPurgeRowsDeletedTotal, "the call must not create a collector")
}

const registrationConflictChildEnv = "OBSERVABILITY_REGISTRATION_CONFLICT_CHILD"

const registrationConflictChildMarker = "CHILD-RESULT:"

// TestRegister_NameAlreadyRegistered_PanicsWithTheCollectorError re-runs this
// test binary as a child process, because Register runs once per process and
// the shared registry cannot be reset.
func TestRegister_NameAlreadyRegistered_PanicsWithTheCollectorError(t *testing.T) {
	if os.Getenv(registrationConflictChildEnv) == "1" {
		runRegistrationConflictChild()
		return
	}

	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(self, "-test.run=^TestRegister_NameAlreadyRegistered_PanicsWithTheCollectorError$")
	cmd.Env = append(os.Environ(), registrationConflictChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "child output:\n%s", out)

	var result string
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(line, registrationConflictChildMarker); ok {
			result = rest
		}
	}
	require.NotEmptyf(t, result, "child did not report a result:\n%s", out)
	require.True(t, strings.HasPrefix(result, "panic=metrics: register service collector: "), result)
	require.Contains(t, result, "workflow_definition_service_submissions_total")
	require.Contains(t, result, "submissions=nil")
}

func runRegistrationConflictChild() {
	log, err := logger.NewLogger("dev")
	if err != nil {
		fmt.Printf("%sunexpected logger error: %v\n", registrationConflictChildMarker, err)
		return
	}
	_ = gincommon.ObservabilityMiddlewares(gincommon.Config{
		Logger: log, ServiceName: "workflow-definition-service", BuildVersion: "test",
		Domain: observability.Domain, Environment: "test",
	})
	gincommon.MetricsRegisterer().MustRegister(prometheus.NewCounter(prometheus.CounterOpts{
		Name: "workflow_definition_service_submissions_total",
		Help: "Registered first by the test.",
	}))

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		observability.Register()
	}()
	fmt.Printf("%spanic=%v submissions=%s\n", registrationConflictChildMarker, recovered,
		map[bool]string{true: "nil", false: "set"}[observability.WFSubmissionsTotal == nil])
}
