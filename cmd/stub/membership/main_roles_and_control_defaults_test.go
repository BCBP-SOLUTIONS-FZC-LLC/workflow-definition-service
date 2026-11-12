package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCmdMemEligibility_RolesHeaderIsNotExactlyIamSystem_Returns403(t *testing.T) {
	cmdMemStart(t)

	cases := []struct {
		name  string
		roles string
	}{
		{"iam-system listed with another role", "iam-system,other"},
		{"iam-system listed second", "other,iam-system"},
		{"upper case", "IAM-SYSTEM"},
		{"leading space", " iam-system"},
		{"trailing space", "iam-system "},
		{"another role only", "tenant_admin"},
		{"empty value", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := cmdMemNewValid().eligibility("reviewer")
			req.headers[cmdMemRolesHeader] = tc.roles

			rec := cmdMemDo(req)

			cmdMemAssertProblem(t, rec, http.StatusForbidden, "FORBIDDEN")
		})
	}
}

func TestCmdMemControl_FieldsLeftOut_AreReadAsFalseAndResetTheStub(t *testing.T) {
	cmdMemStart(t)

	cases := []struct {
		name            string
		body            string
		wantUnavailable bool
	}{
		{"empty object", `{}`, false},
		{"only unavailable false", `{"unavailable":false}`, false},
		{"only unavailable true", `{"unavailable":true}`, true},
		{"unknown fields are ignored", `{"unknown":1}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ineligible.Store(false)
			unavailable.Store(false)

			ctl := cmdMemControl(http.MethodPost, tc.body)

			require.Equal(t, http.StatusOK, ctl.Code)
			assert.Empty(t, ctl.Body.String())
			assert.True(t, ineligible.Load(), "an omitted eligible flag is false, so the stub turns ineligible")
			rec := cmdMemDo(cmdMemNewValid().eligibility("reviewer"))
			if tc.wantUnavailable {
				cmdMemAssertProblem(t, rec, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE")
				return
			}
			require.Equal(t, http.StatusOK, rec.Code)
			got := cmdMemDecode(t, rec)
			assert.Equal(t, false, got["eligible"])
			assert.Equal(t, "not_in_department", got["reason"])
		})
	}
}

func TestCmdMemControl_OmittedFlagsOverwriteTheStateSetBefore(t *testing.T) {
	cmdMemStart(t)
	require.Equal(t, http.StatusOK, cmdMemControl(http.MethodPost, `{"eligible":true,"unavailable":true}`).Code)
	require.Equal(t, http.StatusServiceUnavailable, cmdMemDo(cmdMemNewValid().eligibility("reviewer")).Code)

	ctl := cmdMemControl(http.MethodPost, `{"eligible":true}`)

	require.Equal(t, http.StatusOK, ctl.Code)
	rec := cmdMemDo(cmdMemNewValid().eligibility("reviewer"))
	require.Equal(t, http.StatusOK, rec.Code, "the omitted unavailable flag cleared the outage")
	assert.Equal(t, true, cmdMemDecode(t, rec)["eligible"])
}
