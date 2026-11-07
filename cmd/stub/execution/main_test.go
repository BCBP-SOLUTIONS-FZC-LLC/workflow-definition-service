package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	cmdExeHelperEnv     = "CMDEXE_STUB_HELPER_MODE"
	cmdExeModeBadAddr   = "bad-addr"
	cmdExeModeReportFlg = "report-flag"
	cmdExeListenMarker  = "execution stub listening"
	cmdExeFlagPrefix    = "cmdexe-active="
	cmdExeChildTimeout  = 60 * time.Second
)

// TestMain lets the test binary re-run itself as the stub process, so startup
// behaviour (environment handling, exit codes) can be observed from outside.
func TestMain(m *testing.M) {
	switch os.Getenv(cmdExeHelperEnv) {
	case cmdExeModeBadAddr:
		main()
		os.Exit(0)
	case cmdExeModeReportFlg:
		go main()
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		fmt.Printf("%s%t\n", cmdExeFlagPrefix, active.Load())
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type cmdExeLogEntry struct {
	msg    string
	fields map[string]any
}

type cmdExeRecorder struct {
	mu      sync.Mutex
	entries []cmdExeLogEntry
}

func (r *cmdExeRecorder) record(msg string, fields map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, cmdExeLogEntry{msg: msg, fields: fields})
}

func (r *cmdExeRecorder) Debug(msg string, fields map[string]any) { r.record(msg, fields) }
func (r *cmdExeRecorder) Info(msg string, fields map[string]any)  { r.record(msg, fields) }
func (r *cmdExeRecorder) Warn(msg string, fields map[string]any)  { r.record(msg, fields) }
func (r *cmdExeRecorder) Error(msg string, fields map[string]any) { r.record(msg, fields) }

func (r *cmdExeRecorder) all() []cmdExeLogEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]cmdExeLogEntry(nil), r.entries...)
}

// cmdExeSetup installs a recording logger and a cleared flag, and restores
// the package state when the test ends.
func cmdExeSetup(t *testing.T) *cmdExeRecorder {
	t.Helper()
	prevLogger, prevActive := lg, active.Load()
	rec := &cmdExeRecorder{}
	lg = rec
	active.Store(false)
	t.Cleanup(func() {
		lg = prevLogger
		active.Store(prevActive)
	})
	return rec
}

func cmdExeDo(h http.HandlerFunc, method, target, body string, pathValues map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range pathValues {
		req.SetPathValue(k, v)
	}
	rr := httptest.NewRecorder()
	h(rr, req)
	return rr
}

func cmdExeActiveBody(t *testing.T, rr *httptest.ResponseRecorder) (bool, int) {
	t.Helper()
	var got struct {
		HasActive bool `json:"has_active"`
		Count     int  `json:"count"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got), "body: %s", rr.Body.String())
	return got.HasActive, got.Count
}

func TestStubExecution_ActiveInstances_ReportsFlagState(t *testing.T) {
	cases := []struct {
		name       string
		flag       bool
		wantActive bool
		wantCount  int
	}{
		{name: "flag_unset_reports_none", flag: false, wantActive: false, wantCount: 0},
		{name: "flag_set_reports_exactly_one", flag: true, wantActive: true, wantCount: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := cmdExeSetup(t)
			active.Store(tc.flag)

			rr := cmdExeDo(activeInstances, http.MethodGet,
				"/api/v1/internal/workflows/wf-1/active-instances?tenant_id=tenant-7", "",
				map[string]string{"workflow_id": "wf-1"})

			assert.Equal(t, http.StatusOK, rr.Code)
			assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
			gotActive, gotCount := cmdExeActiveBody(t, rr)
			assert.Equal(t, tc.wantActive, gotActive)
			assert.Equal(t, tc.wantCount, gotCount)

			entries := rec.all()
			require.Len(t, entries, 1)
			assert.Equal(t, "active-instances", entries[0].msg)
			assert.Equal(t, "tenant-7", entries[0].fields["tenant_id"])
			assert.Equal(t, "wf-1", entries[0].fields["workflow_id"])
			assert.Equal(t, tc.wantActive, entries[0].fields["has_active"])
		})
	}
}

func TestStubExecution_ActiveInstances_IgnoresTenantAndWorkflow(t *testing.T) {
	cases := []struct {
		name       string
		target     string
		workflowID string
	}{
		{name: "no_tenant_and_empty_workflow", target: "/x", workflowID: ""},
		{name: "other_tenant_and_workflow", target: "/x?tenant_id=another", workflowID: "wf-other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmdExeSetup(t)
			active.Store(true)

			rr := cmdExeDo(activeInstances, http.MethodGet, tc.target, "",
				map[string]string{"workflow_id": tc.workflowID})

			assert.Equal(t, http.StatusOK, rr.Code)
			gotActive, gotCount := cmdExeActiveBody(t, rr)
			assert.True(t, gotActive)
			assert.Equal(t, 1, gotCount)
		})
	}
}

func TestStubExecution_PauseTasks_ValidBody_Returns204(t *testing.T) {
	rec := cmdExeSetup(t)

	rr := cmdExeDo(pauseTasks, http.MethodPost, "/api/v1/internal/users/user-9/pause-tasks",
		`{"tenant_id":"tenant-3"}`, map[string]string{"user_id": "user-9"})

	assert.Equal(t, http.StatusNoContent, rr.Code)
	assert.Empty(t, rr.Body.String())

	entries := rec.all()
	require.Len(t, entries, 1)
	assert.Equal(t, "pause-tasks", entries[0].msg)
	assert.Equal(t, "tenant-3", entries[0].fields["tenant_id"])
	assert.Equal(t, "user-9", entries[0].fields["user_id"])
}

func TestStubExecution_Control_SetsFlagToLatestValue(t *testing.T) {
	cmdExeSetup(t)

	steps := []struct {
		name       string
		body       string
		wantActive bool
		wantCount  int
	}{
		{name: "turn_on", body: `{"has_active":true}`, wantActive: true, wantCount: 1},
		{name: "turn_off", body: `{"has_active":false}`, wantActive: false, wantCount: 0},
		{name: "turn_on_again", body: `{"has_active":true}`, wantActive: true, wantCount: 1},
	}
	for _, s := range steps {
		rr := cmdExeDo(control, http.MethodPost, "/control", s.body, nil)
		require.Equal(t, http.StatusOK, rr.Code, s.name)

		got := cmdExeDo(activeInstances, http.MethodGet, "/x", "", map[string]string{"workflow_id": "wf"})
		gotActive, gotCount := cmdExeActiveBody(t, got)
		assert.Equal(t, s.wantActive, gotActive, s.name)
		assert.Equal(t, s.wantCount, gotCount, s.name)
	}
}

func TestStubExecution_Control_InvalidBody_Returns400AndKeepsFlag(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "empty_body", body: ""},
		{name: "malformed_json", body: `{"has_active":`},
		{name: "wrong_field_type", body: `{"has_active":"yes"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := cmdExeSetup(t)
			active.Store(true)

			rr := cmdExeDo(control, http.MethodPost, "/control", tc.body, nil)

			assert.Equal(t, http.StatusBadRequest, rr.Code)
			assert.True(t, active.Load(), "flag must be unchanged")
			assert.Empty(t, rec.all(), "a rejected request logs nothing")
		})
	}
}

func TestStubExecution_Control_BodyWithoutField_ResetsFlagToFalse(t *testing.T) {
	cmdExeSetup(t)
	active.Store(true)

	rr := cmdExeDo(control, http.MethodPost, "/control", `{}`, nil)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.False(t, active.Load())
}

// cmdExeRunChild starts the test binary as the stub process in the given mode
// with HAS_ACTIVE and STUB_ADDR controlled by the caller.
func cmdExeRunChild(t *testing.T, mode, hasActive, stubAddr string) (*exec.Cmd, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cmdExeChildTimeout)
	t.Cleanup(cancel)

	exe, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(ctx, exe, "-test.run=^$")
	env := cmdExeKeptEnv()
	env = append(env, "LOG_LEVEL=info", cmdExeHelperEnv+"="+mode, "HAS_ACTIVE="+hasActive, "STUB_ADDR="+stubAddr)
	cmd.Env = env
	return cmd, ctx
}

func TestStubExecution_Startup_ListenFailure_LogsAndExitsWithCode1(t *testing.T) {
	cmd, _ := cmdExeRunChild(t, cmdExeModeBadAddr, "", "not-a-valid-listen-address")

	out, err := cmd.CombinedOutput()

	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr), "expected a non-zero exit, got %v\n%s", err, out)
	assert.Equal(t, 1, exitErr.ExitCode(), string(out))
	assert.Contains(t, string(out), "serve failed")
	assert.Contains(t, string(out), cmdExeListenMarker)
}

func TestStubExecution_Startup_HasActiveEnv_OnlyExactTrueSetsFlag(t *testing.T) {
	cases := []struct {
		name      string
		hasActive string
		want      bool
	}{
		{name: "exact_true_sets_flag", hasActive: "true", want: true},
		{name: "upper_case_ignored", hasActive: "TRUE", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd, _ := cmdExeRunChild(t, cmdExeModeReportFlg, tc.hasActive, "127.0.0.1:0")
			stdin, err := cmd.StdinPipe()
			require.NoError(t, err)
			r, w, err := os.Pipe()
			require.NoError(t, err)
			cmd.Stdout, cmd.Stderr = w, w
			require.NoError(t, cmd.Start())
			require.NoError(t, w.Close())
			t.Cleanup(func() { _ = r.Close() })

			var (
				signalled bool
				reported  = -1
				lines     []string
			)
			sc := bufio.NewScanner(r)
			for sc.Scan() {
				line := sc.Text()
				lines = append(lines, line)
				if !signalled && strings.Contains(line, cmdExeListenMarker) {
					signalled = true
					_, werr := fmt.Fprintln(stdin, "go")
					require.NoError(t, werr)
					require.NoError(t, stdin.Close())
				}
				if v, ok := strings.CutPrefix(line, cmdExeFlagPrefix); ok {
					reported = 0
					if v == "true" {
						reported = 1
					}
				}
			}
			require.NoError(t, cmd.Wait(), strings.Join(lines, "\n"))
			require.True(t, signalled, "stub never logged its listening line:\n%s", strings.Join(lines, "\n"))
			require.NotEqual(t, -1, reported, "child did not report the flag:\n%s", strings.Join(lines, "\n"))
			assert.Equal(t, tc.want, reported == 1)
		})
	}
}

func cmdExeKeptEnv() []string {
	keep := map[string]bool{}
	for _, k := range strings.Fields("GOCOVERDIR GOTOOLCHAIN GOFLAGS GOMODCACHE GOPATH GOCACHE HOME LANG PATH TMPDIR TZ") {
		keep[k] = true
	}
	var env []string
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); ok && keep[k] {
			env = append(env, kv)
		}
	}
	return env
}
