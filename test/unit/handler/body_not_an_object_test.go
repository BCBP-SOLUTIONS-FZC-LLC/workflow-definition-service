package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBodyThatIsNotAnObject_NamesTheParameterBody(t *testing.T) {
	t.Parallel()

	bodies := map[string]string{
		"an empty array": `[]`,
		"an array":       `[{"name":"x"}]`,
		"a string":       `"text"`,
		"a number":       `5`,
		"a boolean":      `true`,
	}
	for _, route := range nulRoutes() {
		for name, raw := range bodies {
			t.Run(route.name+"/"+name, func(t *testing.T) {
				t.Parallel()

				var reached bool
				w := do(newNulRouterEngine(&reached), platformOperatorReq(route.method, route.path, json.RawMessage(raw)))

				require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
				assert.False(t, reached, "a body that is not an object must not reach the service")
				p := decodeNulProblem(t, w.Body.Bytes())
				assert.Equal(t, "BAD_REQUEST", p.Code)
				assert.Equal(t, "request body has a field of the wrong type", p.Detail)
				require.Len(t, p.InvalidParams, 1)
				assert.Equal(t, "body", p.InvalidParams[0].Name)
				assert.Equal(t, "has the wrong type", p.InvalidParams[0].Reason)
			})
		}
	}
}

func TestFieldOfTheWrongType_StillNamesTheField(t *testing.T) {
	t.Parallel()

	var reached bool
	w := do(newNulRouterEngine(&reached), platformOperatorReq(http.MethodPost, "/api/v1/workflows",
		map[string]any{"key": "k", "name": 5, "bpmn_xml": "<x/>"}))

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	p := decodeNulProblem(t, w.Body.Bytes())
	require.Len(t, p.InvalidParams, 1)
	assert.Equal(t, "name", p.InvalidParams[0].Name)
}
