package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	cmdCatPlansPath    = "/api/v1/internal/plans"
	cmdCatHelperEnv    = "CMDCAT_STUB_HELPER"
	cmdCatHelperTest   = "TestCmdCatStub_HelperProcess"
	cmdCatSystemRole   = "iam-system"
	cmdCatRoleHeader   = "x-tenant-roles"
	cmdCatLoopbackAddr = "127.0.0.1:0"
)

var cmdCatStartOnce sync.Once

// cmdCatStartStub runs the stub's main once in this process, listening on an
// OS-assigned loopback port, and waits until its plans route is registered.
// Requests are then served straight through the default mux with a recorder,
// so no client ever needs the listener's address.
func cmdCatStartStub(t *testing.T) {
	t.Helper()
	cmdCatStartOnce.Do(func() {
		t.Setenv("STUB_ADDR", cmdCatLoopbackAddr)
		go main()
	})
	probe := httptest.NewRequest(http.MethodGet, cmdCatPlansPath, nil)
	require.Eventually(t, func() bool {
		_, pattern := http.DefaultServeMux.Handler(probe)
		return pattern != ""
	}, 10*time.Second, time.Millisecond, "stub route was never registered")
}

func cmdCatServe(t *testing.T, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	cmdCatStartStub(t)
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	http.DefaultServeMux.ServeHTTP(rec, req)
	return rec
}

func cmdCatAssertPlanCatalogue(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body struct {
		Plans []map[string]any `json:"plans"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Plans, 3)

	wantCodes := []string{"starter", "pro", "enterprise"}
	for i, code := range wantCodes {
		assert.Equal(t, code, body.Plans[i]["code"])
		_, present := body.Plans[i]["workflow_template_limit"]
		assert.True(t, present, "workflow_template_limit must be present for %s", code)
	}
	assert.EqualValues(t, 5, body.Plans[0]["workflow_template_limit"])
	assert.EqualValues(t, 50, body.Plans[1]["workflow_template_limit"])
	assert.Nil(t, body.Plans[2]["workflow_template_limit"], "enterprise is unlimited and serialises as null")
}

func TestCmdCatStub_PlansWithSystemRole_ReturnsFixedCatalogue(t *testing.T) {
	rec := cmdCatServe(t, http.MethodGet, cmdCatPlansPath, map[string]string{cmdCatRoleHeader: cmdCatSystemRole})

	cmdCatAssertPlanCatalogue(t, rec)
}

func TestCmdCatStub_PlansWithoutSystemRole_Returns403(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
	}{
		{name: "no role header", headers: nil},
		{name: "empty role header", headers: map[string]string{cmdCatRoleHeader: ""}},
		{name: "admin role", headers: map[string]string{cmdCatRoleHeader: "admin"}},
		{name: "upper-case value", headers: map[string]string{cmdCatRoleHeader: "IAM-SYSTEM"}},
		{name: "leading space", headers: map[string]string{cmdCatRoleHeader: " iam-system"}},
		{name: "role list containing system role", headers: map[string]string{cmdCatRoleHeader: "iam-system,admin"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := cmdCatServe(t, http.MethodGet, cmdCatPlansPath, tc.headers)

			assert.Equal(t, http.StatusForbidden, rec.Code)
		})
	}
}

func TestCmdCatStub_OtherPaths_Return404(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "trailing slash", path: cmdCatPlansPath + "/"},
		{name: "plan code below plans", path: cmdCatPlansPath + "/starter"},
		{name: "unrelated path", path: "/healthz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := cmdCatServe(t, http.MethodGet, tc.path, map[string]string{cmdCatRoleHeader: cmdCatSystemRole})

			assert.Equal(t, http.StatusNotFound, rec.Code)
			assert.NotContains(t, rec.Body.String(), "plans\":")
		})
	}
}

// TestCmdCatStub_HelperProcess runs the stub's main in a child process. It does
// nothing unless the parent test sets the helper variable.
func TestCmdCatStub_HelperProcess(t *testing.T) {
	if os.Getenv(cmdCatHelperEnv) != "1" {
		t.Skip("helper process for the exit-code test")
	}
	main()
}

// cmdCatChildEnv is the parent environment without the logging variables a
// developer may have set, plus an info log level and the given extra entries.
func cmdCatChildEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "LOG_FILE="),
			strings.HasPrefix(kv, "LOG_LEVEL="),
			strings.HasPrefix(kv, "LOG_SAMPLING="):
		default:
			env = append(env, kv)
		}
	}
	env = append(env, "LOG_LEVEL=info")
	return append(env, extra...)
}

func TestCmdCatStub_MalformedListenAddress_LogsServeFailedAndExits1(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^"+cmdCatHelperTest+"$")
	cmd.Env = cmdCatChildEnv(cmdCatHelperEnv+"=1", "STUB_ADDR=not:a:valid:address")

	out, err := cmd.CombinedOutput()

	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "child output: %s", out)
	assert.Equal(t, 1, exitErr.ExitCode())
	assert.Contains(t, string(out), "serve failed", "child output: %s", out)
	assert.Contains(t, string(out), "too many colons", "child output: %s", out)
}
