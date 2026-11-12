package handler_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteConnectorCredential_FieldNameOfAllowedCharacters_StoresUnderThatNameAndReturns201(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		fieldName string
	}{
		{name: "mixed case letters digits hyphen and underscore", fieldName: "a-b_C9"},
		{name: "single hyphen", fieldName: "-"},
		{name: "single underscore", fieldName: "_"},
		{name: "single zero", fieldName: "0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := &credentialWriteStore{}
			body := map[string]any{"connector_type": "send-email", "field_name": tc.fieldName, "value": "w7d-secret"}

			w := do(credentialWriteRealHandler(store), adminReq(http.MethodPost, credentialWritePath, body))

			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			wantPath := fmt.Sprintf("connectors/%s/send-email/%s", testTenantID, tc.fieldName)
			assert.Equal(t, map[string]any{"secret_path": wantPath}, decodeProblem(t, w))
			calls := store.calls()
			require.Len(t, calls, 1)
			assert.Equal(t, wantPath, calls[0].path)
			assert.Equal(t, map[string]string{tc.fieldName: "w7d-secret"}, calls[0].data)
		})
	}
}

func TestWriteConnectorCredential_FieldNameLength_AcceptsTheCapAndRefusesOneOverWithAProblemBody(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		length     int
		wantStatus int
	}{
		{name: "128 characters", length: 128, wantStatus: http.StatusCreated},
		{name: "129 characters", length: 129, wantStatus: http.StatusBadRequest},
		{name: "100000 characters", length: 100000, wantStatus: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := &credentialWriteStore{}
			fieldName := strings.Repeat("a", tc.length)
			body := map[string]any{"connector_type": "send-email", "field_name": fieldName, "value": "w7d-secret"}

			w := do(credentialWriteRealHandler(store), adminReq(http.MethodPost, credentialWritePath, body))

			require.Equal(t, tc.wantStatus, w.Code)
			if tc.wantStatus == http.StatusCreated {
				calls := store.calls()
				require.Len(t, calls, 1)
				assert.Equal(t, fmt.Sprintf("connectors/%s/send-email/%s", testTenantID, fieldName), calls[0].path)
				return
			}
			prob := decodeProblem(t, w)
			assert.Equal(t, "INVALID_INPUT", prob["code"])
			assert.Equal(t, float64(http.StatusBadRequest), prob["status"])
			detail, _ := prob["detail"].(string)
			assert.Contains(t, detail, "field_name must match")
			assert.Contains(t, detail, "at most 128 characters")
			assert.Empty(t, store.calls(), "a refused field name never reaches the secrets store")
			assert.NotContains(t, w.Body.String(), "w7d-secret")
			assert.NotContains(t, w.Body.String(), fieldName, "the oversized name is not echoed back")
		})
	}
}
