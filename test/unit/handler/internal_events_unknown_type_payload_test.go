package handler_test

import (
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInternalEvents_AnUnhandledTypeIsAcknowledgedWithoutDecodingItsPayload(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		envelope func() map[string]any
	}{
		{
			name: "registry schema with an undecodable string payload",
			envelope: func() map[string]any {
				return registryEnvelope("SomethingElseHappened", "###")
			},
		},
		{
			name: "registry schema with a frame that is too short",
			envelope: func() map[string]any {
				return registryEnvelope("SomethingElseHappened", base64.StdEncoding.EncodeToString([]byte{0x03, 0x00}))
			},
		},
		{
			name: "registry schema with a plain object payload",
			envelope: func() map[string]any {
				env := envelope("SomethingElseHappened", ieEventID.String(), ieTenantID.String(), map[string]string{"k": "v"})
				env["dataschema"] = uuid.NewString()
				return env
			},
		},
		{
			name: "no schema and a payload that is not an object",
			envelope: func() map[string]any {
				return envelope("SomethingElseHappened", ieEventID.String(), ieTenantID.String(), "just a string")
			},
		},
		{
			name: "empty type with a registry payload that cannot be decoded",
			envelope: func() map[string]any {
				return registryEnvelope("", "###")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rev := &fakeMembershipRevoker{}

			w := postEvent(rev, tc.envelope())

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Empty(t, w.Body.String())
			assert.False(t, rev.called || rev.tenantCalled, "an unhandled type reaches no service")
		})
	}
}
