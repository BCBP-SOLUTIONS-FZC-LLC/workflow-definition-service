package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/v2/pkg/registry"
)

func sgDRegistryBody(t *testing.T, defs map[string]registry.Definition) map[string]map[string]json.RawMessage {
	t.Helper()
	h := newConnectorHandler(&fakeConnectorSvc{
		registryFn: func(context.Context) map[string]registry.Definition { return defs },
	})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/connectors/registry", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Connectors map[string]map[string]json.RawMessage `json:"connectors"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body.Connectors
}

func sgDDecodeFields(t *testing.T, raw json.RawMessage) []map[string]any {
	t.Helper()
	var fields []map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	return fields
}

func TestListConnectorRegistry_RealCatalogue_MapsEveryDefinitionToSnakeCaseFields(t *testing.T) {
	t.Parallel()

	defs := registry.All()
	got := sgDRegistryBody(t, defs)

	require.Len(t, got, len(defs))
	wantRetry := map[string]string{
		registry.TypeStorage:   "safe",
		registry.TypeSendEmail: "not-delivered",
		registry.TypeRestCall:  "conditional",
	}
	for typ, def := range defs {
		def := def
		raw, ok := got[typ]
		require.True(t, ok, typ)
		var retry, displayName, description, gotType string
		require.NoError(t, json.Unmarshal(raw["retry"], &retry))
		require.NoError(t, json.Unmarshal(raw["display_name"], &displayName))
		require.NoError(t, json.Unmarshal(raw["description"], &description))
		require.NoError(t, json.Unmarshal(raw["type"], &gotType))
		assert.Equal(t, wantRetry[typ], retry, typ)
		assert.Equal(t, def.DisplayName, displayName)
		assert.Equal(t, def.Description, description)
		assert.Equal(t, typ, gotType)

		inputs := sgDDecodeFields(t, raw["inputs"])
		require.Len(t, inputs, len(def.Inputs), typ)
		for i, f := range def.Inputs {
			assert.Equal(t, f.Name, inputs[i]["name"])
			assert.Equal(t, string(f.Kind), inputs[i]["kind"])
			assert.Equal(t, f.Required, inputs[i]["required"])
		}
		outputs := sgDDecodeFields(t, raw["outputs"])
		require.Len(t, outputs, len(def.Outputs), typ)
		for i, f := range def.Outputs {
			assert.Equal(t, f.Name, outputs[i]["name"])
			assert.Equal(t, string(f.Kind), outputs[i]["kind"])
		}
		assert.NotContains(t, raw, "Retry")
		assert.NotContains(t, raw, "DisplayName")
	}
}

func TestListConnectorRegistry_OptionalFieldKeysAreOmittedWhenEmptyAndRequiredFalseIsEmitted(t *testing.T) {
	t.Parallel()

	got := sgDRegistryBody(t, map[string]registry.Definition{
		"sgd-type": {
			Type:        "sgd-type",
			DisplayName: "Sgd",
			Inputs: []registry.Field{
				{Name: "plain", Kind: registry.FieldKindString, Required: false},
				{Name: "rich", Kind: registry.FieldKindEnum, Required: true, EnumValues: []string{"a", "b"}, Condition: "provider == a", Description: "pick one"},
			},
			Outputs: []registry.Field{{Name: "out", Kind: registry.FieldKindString}},
			Retry:   registry.RetryPolicySafe,
		},
	})

	inputs := sgDDecodeFields(t, got["sgd-type"]["inputs"])
	require.Len(t, inputs, 2)
	assert.Equal(t, map[string]any{"name": "plain", "kind": "string", "required": false}, inputs[0])
	assert.Equal(t, map[string]any{
		"name": "rich", "kind": "enum", "required": true,
		"enum_values": []any{"a", "b"}, "condition": "provider == a", "description": "pick one",
	}, inputs[1])
	outputs := sgDDecodeFields(t, got["sgd-type"]["outputs"])
	assert.Equal(t, []map[string]any{{"name": "out", "kind": "string", "required": false}}, outputs)
}

func TestListConnectorRegistry_DefinitionWithoutFieldsSerialisesEmptyListsNotNull(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		def  registry.Definition
	}{
		{name: "nil inputs and outputs", def: registry.Definition{Type: "sgd-bare", DisplayName: "Bare", Retry: registry.RetryPolicyUnsafe}},
		{name: "empty inputs and outputs", def: registry.Definition{Type: "sgd-bare", DisplayName: "Bare", Inputs: []registry.Field{}, Outputs: []registry.Field{}, Retry: registry.RetryPolicyUnsafe}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sgDRegistryBody(t, map[string]registry.Definition{"sgd-bare": tc.def})

			assert.JSONEq(t, `[]`, string(got["sgd-bare"]["inputs"]))
			assert.JSONEq(t, `[]`, string(got["sgd-bare"]["outputs"]))
			assert.JSONEq(t, `"unsafe"`, string(got["sgd-bare"]["retry"]))
		})
	}
}
