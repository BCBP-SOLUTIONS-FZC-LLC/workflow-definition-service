package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	cmdMemChildEnv        = "CMDMEM_CHILD"
	cmdMemChildExpectEnv  = "CMDMEM_EXPECT_ELIGIBLE"
	cmdMemRolesHeader     = "x-tenant-roles"
	cmdMemTenantHeader    = "x-tenant-id"
	cmdMemIAMRole         = "iam-system"
	cmdMemProblemType     = "application/problem+json"
	cmdMemChildStartup    = "startup-state"
	cmdMemChildBadAddress = "bad-address"
)

var cmdMemStartOnce sync.Once

// cmdMemLaunch runs the stub's main in the background and waits until both of
// its routes are registered on the default mux. main registers handlers on the
// process-wide default mux, so it can run only once per process.
func cmdMemLaunch(t *testing.T) {
	t.Helper()
	cmdMemStartOnce.Do(func() {
		go main()
	})
	require.Eventually(t, func() bool {
		_, control := http.DefaultServeMux.Handler(httptest.NewRequest(http.MethodPost, "/control", nil))
		_, eligibility := http.DefaultServeMux.Handler(httptest.NewRequest(http.MethodGet, cmdMemEligibilityPath(uuid.NewString(), uuid.NewString()), nil))
		return control != "" && eligibility != ""
	}, 10*time.Second, 5*time.Millisecond, "stub routes were not registered")
}

// cmdMemStart launches the stub on an ephemeral loopback port with the default
// (eligible) state and clears the two run-time flags again when the test ends.
func cmdMemStart(t *testing.T) {
	t.Helper()
	t.Setenv("ELIGIBLE", "")
	t.Setenv("STUB_ADDR", "127.0.0.1:0")
	cmdMemLaunch(t)
	ineligible.Store(false)
	unavailable.Store(false)
	t.Cleanup(func() {
		ineligible.Store(false)
		unavailable.Store(false)
	})
}

func cmdMemEligibilityPath(tenant, user string) string {
	return "/api/v1/internal/tenants/" + tenant + "/members/" + user + "/eligibility"
}

type cmdMemRequest struct {
	method  string
	path    string
	query   url.Values
	headers map[string]string
	body    string
}

type cmdMemValid struct {
	tenant, user, dept string
}

func cmdMemNewValid() cmdMemValid {
	return cmdMemValid{tenant: uuid.NewString(), user: uuid.NewString(), dept: uuid.NewString()}
}

// eligibility builds a request the stub accepts; callers then break one part.
func (v cmdMemValid) eligibility(level string) cmdMemRequest {
	return cmdMemRequest{
		method:  http.MethodGet,
		path:    cmdMemEligibilityPath(v.tenant, v.user),
		query:   url.Values{"department_id": {v.dept}, "required_level": {level}},
		headers: map[string]string{cmdMemRolesHeader: cmdMemIAMRole, cmdMemTenantHeader: v.tenant},
	}
}

func cmdMemDo(r cmdMemRequest) *httptest.ResponseRecorder {
	target := r.path
	if len(r.query) > 0 {
		target += "?" + r.query.Encode()
	}
	req := httptest.NewRequest(r.method, target, strings.NewReader(r.body))
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	http.DefaultServeMux.ServeHTTP(rec, req)
	return rec
}

func cmdMemControl(method, body string) *httptest.ResponseRecorder {
	return cmdMemDo(cmdMemRequest{method: method, path: "/control", body: body})
}

func cmdMemDecode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m), "body: %q", rec.Body.String())
	return m
}

func cmdMemAssertProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	require.Equal(t, status, rec.Code)
	assert.Equal(t, cmdMemProblemType, rec.Header().Get("Content-Type"))
	assert.Equal(t, map[string]any{"status": float64(status), "errorCode": code}, cmdMemDecode(t, rec))
}

func TestCmdMemEligibility_RolesHeaderMissing_Returns403(t *testing.T) {
	cmdMemStart(t)

	cases := []struct {
		name    string
		mutate  func(r *cmdMemRequest)
		wantErr string
	}{
		{
			name:    "roles header absent",
			mutate:  func(r *cmdMemRequest) { delete(r.headers, cmdMemRolesHeader) },
			wantErr: "FORBIDDEN",
		},
		{
			name: "roles header absent and tenant id malformed",
			mutate: func(r *cmdMemRequest) {
				delete(r.headers, cmdMemRolesHeader)
				r.path = cmdMemEligibilityPath("not-a-uuid", uuid.NewString())
			},
			wantErr: "FORBIDDEN",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := cmdMemNewValid().eligibility("reviewer")
			tc.mutate(&req)

			rec := cmdMemDo(req)

			cmdMemAssertProblem(t, rec, http.StatusForbidden, tc.wantErr)
		})
	}
}

func TestCmdMemEligibility_InvalidUUID_Returns400(t *testing.T) {
	cmdMemStart(t)

	cases := []struct {
		name   string
		mutate func(r *cmdMemRequest, v cmdMemValid)
	}{
		{"bad tenant id", func(r *cmdMemRequest, v cmdMemValid) {
			r.path = cmdMemEligibilityPath("not-a-uuid", v.user)
			r.headers[cmdMemTenantHeader] = "not-a-uuid"
		}},
		{"bad user id", func(r *cmdMemRequest, v cmdMemValid) { r.path = cmdMemEligibilityPath(v.tenant, "not-a-uuid") }},
		{"bad department id", func(r *cmdMemRequest, v cmdMemValid) { r.query.Set("department_id", "not-a-uuid") }},
		{"department id absent", func(r *cmdMemRequest, v cmdMemValid) { r.query.Del("department_id") }},
		{"department id empty", func(r *cmdMemRequest, v cmdMemValid) { r.query.Set("department_id", "") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := cmdMemNewValid()
			req := v.eligibility("reviewer")
			tc.mutate(&req, v)

			rec := cmdMemDo(req)

			cmdMemAssertProblem(t, rec, http.StatusBadRequest, "invalid_uuid")
		})
	}
}

func TestCmdMemEligibility_RequiredLevel_ClosedCaseSensitiveSet(t *testing.T) {
	cmdMemStart(t)

	invalid := []struct {
		name  string
		level *string
	}{
		{"absent", nil},
		{"empty", new("")},
		{"capitalised", new("Reviewer")},
		{"unknown", new("admin")},
	}
	for _, tc := range invalid {
		t.Run("invalid "+tc.name, func(t *testing.T) {
			req := cmdMemNewValid().eligibility("reviewer")
			if tc.level == nil {
				req.query.Del("required_level")
			} else {
				req.query.Set("required_level", *tc.level)
			}

			rec := cmdMemDo(req)

			cmdMemAssertProblem(t, rec, http.StatusBadRequest, "invalid_role_level")
		})
	}

	for _, level := range []string{"preparator", "reviewer", "approver"} {
		t.Run("valid "+level, func(t *testing.T) {
			v := cmdMemNewValid()

			rec := cmdMemDo(v.eligibility(level))

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, level, cmdMemDecode(t, rec)["required_level"])
		})
	}
}

func TestCmdMemEligibility_TenantHeaderMismatch_Returns400(t *testing.T) {
	cmdMemStart(t)

	cases := []struct {
		name   string
		mutate func(r *cmdMemRequest, v cmdMemValid)
		code   string
	}{
		{"header absent", func(r *cmdMemRequest, v cmdMemValid) { delete(r.headers, cmdMemTenantHeader) }, "TENANT_MISMATCH"},
		{"header names another tenant", func(r *cmdMemRequest, v cmdMemValid) {
			r.headers[cmdMemTenantHeader] = uuid.NewString()
		}, "TENANT_MISMATCH"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := cmdMemNewValid()
			req := v.eligibility("approver")
			tc.mutate(&req, v)

			rec := cmdMemDo(req)

			cmdMemAssertProblem(t, rec, http.StatusBadRequest, tc.code)
		})
	}
}

func TestCmdMemEligibility_DefaultState_ReturnsEligibleWithoutReason(t *testing.T) {
	cmdMemStart(t)
	v := cmdMemNewValid()

	rec := cmdMemDo(v.eligibility("preparator"))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, map[string]any{
		"tenant_id":      v.tenant,
		"user_id":        v.user,
		"department_id":  v.dept,
		"required_level": "preparator",
		"eligible":       true,
	}, cmdMemDecode(t, rec))
}

func TestCmdMemEligibility_PostMethod_Returns405(t *testing.T) {
	cmdMemStart(t)
	req := cmdMemNewValid().eligibility("reviewer")
	req.method = http.MethodPost

	rec := cmdMemDo(req)

	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Contains(t, rec.Header().Get("Allow"), http.MethodGet)
	assert.NotEqual(t, cmdMemProblemType, rec.Header().Get("Content-Type"))
}

func TestCmdMemEligibility_UnregisteredPath_Returns404WithoutRoleCheck(t *testing.T) {
	cmdMemStart(t)
	v := cmdMemNewValid()

	cases := []struct {
		name string
		path string
	}{
		{"unknown path", "/other"},
		{"user segment missing", "/api/v1/internal/tenants/" + v.tenant + "/members/eligibility"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := cmdMemDo(cmdMemRequest{method: http.MethodGet, path: tc.path})

			assert.Equal(t, http.StatusNotFound, rec.Code)
			assert.NotEqual(t, cmdMemProblemType, rec.Header().Get("Content-Type"))
		})
	}
}

func TestCmdMemEligibility_ServiceUnavailable_Returns503(t *testing.T) {
	cmdMemStart(t)
	require.Equal(t, http.StatusOK, cmdMemControl(http.MethodPost, `{"eligible":true,"unavailable":true}`).Code)

	cases := []struct {
		name       string
		mutate     func(r *cmdMemRequest)
		wantStatus int
		wantCode   string
	}{
		{"valid request", func(r *cmdMemRequest) {}, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE"},
		{"missing role still forbidden", func(r *cmdMemRequest) { delete(r.headers, cmdMemRolesHeader) }, http.StatusForbidden, "FORBIDDEN"},
		{"bad level still rejected", func(r *cmdMemRequest) { r.query.Set("required_level", "admin") }, http.StatusBadRequest, "invalid_role_level"},
		{"tenant mismatch still rejected", func(r *cmdMemRequest) { r.headers[cmdMemTenantHeader] = uuid.NewString() }, http.StatusBadRequest, "TENANT_MISMATCH"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := cmdMemNewValid().eligibility("reviewer")
			tc.mutate(&req)

			rec := cmdMemDo(req)

			cmdMemAssertProblem(t, rec, tc.wantStatus, tc.wantCode)
		})
	}
}

func TestCmdMemEligibility_UnavailableAndIneligible_Returns503(t *testing.T) {
	cmdMemStart(t)
	require.Equal(t, http.StatusOK, cmdMemControl(http.MethodPost, `{"eligible":false,"unavailable":true}`).Code)

	rec := cmdMemDo(cmdMemNewValid().eligibility("reviewer"))

	cmdMemAssertProblem(t, rec, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE")
	assert.NotContains(t, cmdMemDecode(t, rec), "eligible")
}

func TestCmdMemControl_Eligible_SwitchesNextAnswer(t *testing.T) {
	cmdMemStart(t)

	cases := []struct {
		name         string
		body         string
		wantEligible bool
	}{
		{"ineligible", `{"eligible":false,"unavailable":false}`, false},
		{"eligible again", `{"eligible":true,"unavailable":false}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctl := cmdMemControl(http.MethodPost, tc.body)
			require.Equal(t, http.StatusOK, ctl.Code)
			assert.Empty(t, ctl.Body.String())

			v := cmdMemNewValid()
			rec := cmdMemDo(v.eligibility("reviewer"))

			require.Equal(t, http.StatusOK, rec.Code)
			got := cmdMemDecode(t, rec)
			assert.Equal(t, tc.wantEligible, got["eligible"])
			if tc.wantEligible {
				assert.NotContains(t, got, "reason")
			} else {
				assert.Equal(t, "not_in_department", got["reason"])
			}
		})
	}
}

func TestCmdMemControl_Ineligible_AnswersEveryLevelAndUserWithReason(t *testing.T) {
	cmdMemStart(t)
	require.Equal(t, http.StatusOK, cmdMemControl(http.MethodPost, `{"eligible":false}`).Code)

	for _, level := range []string{"preparator", "reviewer", "approver"} {
		for range 2 {
			v := cmdMemNewValid()

			rec := cmdMemDo(v.eligibility(level))

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
			assert.Equal(t, map[string]any{
				"tenant_id":      v.tenant,
				"user_id":        v.user,
				"department_id":  v.dept,
				"required_level": level,
				"eligible":       false,
				"reason":         "not_in_department",
			}, cmdMemDecode(t, rec))
		}
	}
}

func TestCmdMemControl_ResetAfterUnavailable_ServesEligibleAgain(t *testing.T) {
	cmdMemStart(t)
	require.Equal(t, http.StatusOK, cmdMemControl(http.MethodPost, `{"eligible":true,"unavailable":true}`).Code)
	require.Equal(t, http.StatusServiceUnavailable, cmdMemDo(cmdMemNewValid().eligibility("reviewer")).Code)

	ctl := cmdMemControl(http.MethodPost, `{"eligible":true,"unavailable":false}`)

	require.Equal(t, http.StatusOK, ctl.Code)
	rec := cmdMemDo(cmdMemNewValid().eligibility("reviewer"))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, true, cmdMemDecode(t, rec)["eligible"])
}

func TestCmdMemControl_UndecodableBody_Returns400AndKeepsState(t *testing.T) {
	cmdMemStart(t)
	require.Equal(t, http.StatusOK, cmdMemControl(http.MethodPost, `{"eligible":false,"unavailable":false}`).Code)

	cases := []struct {
		name string
		body string
	}{
		{"invalid json", `{"eligible":`},
		{"empty body", ``},
		{"wrong type", `{"eligible":"yes","unavailable":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctl := cmdMemControl(http.MethodPost, tc.body)

			require.Equal(t, http.StatusBadRequest, ctl.Code)
			assert.Empty(t, ctl.Body.String())
			rec := cmdMemDo(cmdMemNewValid().eligibility("reviewer"))
			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, false, cmdMemDecode(t, rec)["eligible"])
		})
	}
}

func TestCmdMemControl_NonPostMethod_Returns405AndKeepsState(t *testing.T) {
	cmdMemStart(t)
	require.Equal(t, http.StatusOK, cmdMemControl(http.MethodPost, `{"eligible":false,"unavailable":false}`).Code)

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			ctl := cmdMemControl(method, `{"eligible":true,"unavailable":true}`)

			require.Equal(t, http.StatusMethodNotAllowed, ctl.Code)
			assert.Empty(t, ctl.Body.String())
			rec := cmdMemDo(cmdMemNewValid().eligibility("reviewer"))
			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, false, cmdMemDecode(t, rec)["eligible"])
		})
	}
}

func TestCmdMemControl_ConcurrentWithEligibility_NoRaceAndSettlesOnLastWrite(t *testing.T) {
	cmdMemStart(t)
	const rounds = 50

	var wg sync.WaitGroup
	statuses := make([][]int, 4)
	controlStatuses := make([]int, 0, rounds*2)
	wg.Go(func() {
		for range rounds {
			controlStatuses = append(controlStatuses,
				cmdMemControl(http.MethodPost, `{"eligible":false,"unavailable":false}`).Code,
				cmdMemControl(http.MethodPost, `{"eligible":true,"unavailable":true}`).Code)
		}
	})
	for i := range statuses {
		wg.Go(func() {
			v := cmdMemNewValid()
			for range rounds {
				statuses[i] = append(statuses[i], cmdMemDo(v.eligibility("reviewer")).Code)
			}
		})
	}
	wg.Wait()

	for _, code := range controlStatuses {
		assert.Equal(t, http.StatusOK, code)
	}
	for _, list := range statuses {
		for _, code := range list {
			assert.Contains(t, []int{http.StatusOK, http.StatusServiceUnavailable}, code)
		}
	}
	require.Equal(t, http.StatusOK, cmdMemControl(http.MethodPost, `{"eligible":false,"unavailable":false}`).Code)
	rec := cmdMemDo(cmdMemNewValid().eligibility("reviewer"))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, false, cmdMemDecode(t, rec)["eligible"])
}

// TestCmdMemChildProcess is the body run inside the child processes started by
// the tests below; without the child env var it does nothing.
func TestCmdMemChildProcess(t *testing.T) {
	switch os.Getenv(cmdMemChildEnv) {
	case "":
		t.Skip("runs only as a child of another test")
	case cmdMemChildStartup:
		cmdMemLaunch(t)
		rec := cmdMemDo(cmdMemNewValid().eligibility("reviewer"))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, os.Getenv(cmdMemChildExpectEnv) == "true", cmdMemDecode(t, rec)["eligible"])
	case cmdMemChildBadAddress:
		main()
		t.Fatal("main returned instead of exiting")
	default:
		t.Fatalf("unknown child mode %q", os.Getenv(cmdMemChildEnv))
	}
}

// cmdMemChildEnvironment is the parent environment without the logging
// variables a developer may have set, plus an info log level and the extras.
func cmdMemChildEnvironment(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "LOG_FILE=") || strings.HasPrefix(kv, "LOG_LEVEL=") ||
			strings.HasPrefix(kv, "LOG_SAMPLING=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "LOG_LEVEL=info")
	return append(env, extra...)
}

func cmdMemRunChild(t *testing.T, env ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	exe, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestCmdMemChildProcess$", "-test.v", "-test.count=1")
	cmd.Env = cmdMemChildEnvironment(env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestCmdMemStartup_EligibleEnv_SetsInitialAnswer(t *testing.T) {
	cases := []struct {
		eligibleEnv  string
		wantEligible bool
	}{
		{"false", false},
		{"FALSE", true},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("ELIGIBLE=%q", tc.eligibleEnv), func(t *testing.T) {
			t.Parallel()
			out, err := cmdMemRunChild(t,
				cmdMemChildEnv+"="+cmdMemChildStartup,
				cmdMemChildExpectEnv+"="+fmt.Sprint(tc.wantEligible),
				"ELIGIBLE="+tc.eligibleEnv,
				"STUB_ADDR=127.0.0.1:0",
			)

			require.NoError(t, err, out)
			assert.Contains(t, out, "--- PASS: TestCmdMemChildProcess")
		})
	}
}

func TestCmdMemStartup_UnusableListenAddress_LogsAndExitsWithCode1(t *testing.T) {
	out, err := cmdMemRunChild(t,
		cmdMemChildEnv+"="+cmdMemChildBadAddress,
		"STUB_ADDR=127.0.0.1:not-a-port",
	)

	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, out)
	assert.Equal(t, 1, exitErr.ExitCode())
	assert.Contains(t, out, "serve failed")
	assert.Contains(t, out, "not-a-port")
}
