package handler_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func glueFrame(version, compression byte, payload []byte) string {
	id := uuid.New()
	frame := append([]byte{version, compression}, id[:]...)
	return base64.StdEncoding.EncodeToString(append(frame, payload...))
}

func registryEnvelope(eventType string, framed string) map[string]any {
	env := envelope(eventType, ieEventID.String(), ieTenantID.String(), framed)
	env["dataschema"] = uuid.NewString()
	return env
}

func TestInternalEvents_SchemaRegistryPayload_IsDecodedAndHandled(t *testing.T) {
	body, err := json.Marshal(map[string]string{"user_id": ieUserID.String(), "department_id": ieDeptID, "role": "reviewer"})
	require.NoError(t, err)
	rev := &fakeMembershipRevoker{}

	w := postEvent(rev, registryEnvelope("DepartmentMembershipRevoked", glueFrame(0x03, 0x00, body)))

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.True(t, rev.called, "a schema-registry framed payload must reach the service decoded")
	assert.Equal(t, ieUserID, rev.gotUserID)
	assert.Equal(t, ieDeptID, rev.gotDeptID)
}

func TestInternalEvents_SchemaRegistryPayload_TenantRevocationIsDecoded(t *testing.T) {
	body, err := json.Marshal(map[string]string{"user_id": ieUserID.String()})
	require.NoError(t, err)
	rev := &fakeMembershipRevoker{}

	w := postEvent(rev, registryEnvelope("MembershipRevoked", glueFrame(0x03, 0x00, body)))

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.True(t, rev.tenantCalled)
}

func TestInternalEvents_SchemaRegistryPayload_UndecodableIs502(t *testing.T) {
	valid := []byte(`{"user_id":"` + ieUserID.String() + `"}`)
	for name, framed := range map[string]string{
		"not base64":          "###",
		"wrong header":        glueFrame(0x01, 0x00, valid),
		"compressed":          glueFrame(0x03, 0x05, valid),
		"not json":            glueFrame(0x03, 0x00, []byte("\x00\x01binary")),
		"shorter than header": base64.StdEncoding.EncodeToString([]byte{0x03, 0x00}),
	} {
		t.Run(name, func(t *testing.T) {
			rev := &fakeMembershipRevoker{}
			w := postEvent(rev, registryEnvelope("MembershipRevoked", framed))

			assert.Equal(t, http.StatusBadGateway, w.Code, w.Body.String())
			var resp struct{ Code string }
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, "EVENT_DECODE_FAILED", resp.Code)
			assert.False(t, rev.tenantCalled || rev.called)
		})
	}
}

func TestInternalEvents_SchemaRegistryPayload_NotAStringIs502(t *testing.T) {
	env := envelope("MembershipRevoked", ieEventID.String(), ieTenantID.String(), map[string]string{"user_id": ieUserID.String()})
	env["dataschema"] = uuid.NewString()
	rev := &fakeMembershipRevoker{}

	w := postEvent(rev, env)

	assert.Equal(t, http.StatusBadGateway, w.Code, "a dataschema with a plain object payload is not a registry frame")
	assert.False(t, rev.tenantCalled)
}
