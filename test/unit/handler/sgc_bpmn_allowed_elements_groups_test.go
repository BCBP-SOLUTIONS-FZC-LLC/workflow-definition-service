package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sgCAllowedElements(t *testing.T) []string {
	t.Helper()
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/bpmn/allowed-elements", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	require.Len(t, raw, 1, "the body carries only the elements key")
	var elements []string
	require.NoError(t, json.Unmarshal(raw["elements"], &elements))
	return elements
}

func TestAllowedBPMNElements_Body_NamesEachElementGroupAndExcludesUnsupportedElements(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		present []string
		absent  []string
	}{
		{
			name:    "structure elements",
			present: []string{"definitions", "collaboration", "participant", "process", "laneSet", "lane", "flowNodeRef"},
		},
		{
			name:    "task elements",
			present: []string{"task", "userTask", "serviceTask", "sendTask", "receiveTask", "callActivity", "subProcess"},
		},
		{
			name:    "event elements",
			present: []string{"startEvent", "endEvent", "boundaryEvent", "timerEventDefinition", "errorEventDefinition", "messageEventDefinition"},
		},
		{
			name:    "gateway elements",
			present: []string{"parallelGateway", "exclusiveGateway", "inclusiveGateway"},
		},
		{
			name:    "flow elements",
			present: []string{"sequenceFlow", "messageFlow", "conditionExpression"},
		},
		{
			name:   "elements outside the allowlist",
			absent: []string{"scriptTask", "businessRuleTask", "manualTask", "eventBasedGateway", "complexGateway", "transaction", "adHocSubProcess"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			elements := sgCAllowedElements(t)

			for _, want := range tc.present {
				assert.Contains(t, elements, want)
			}
			for _, banned := range tc.absent {
				assert.NotContains(t, elements, banned)
			}
		})
	}
}

func TestAllowedBPMNElements_Body_EntriesAreUniqueAndNonEmpty(t *testing.T) {
	t.Parallel()

	elements := sgCAllowedElements(t)

	seen := map[string]bool{}
	for _, e := range elements {
		assert.NotEmpty(t, e)
		assert.False(t, seen[e], "element %q is listed twice", e)
		seen[e] = true
	}
}
