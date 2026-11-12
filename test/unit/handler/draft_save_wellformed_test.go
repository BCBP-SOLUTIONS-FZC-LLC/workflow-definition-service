package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveDraft_BPMNXMLThatIsNotWellFormed_Is400InvalidBPMNXML(t *testing.T) {
	for _, xml := range []string{"", "not xml", "<a><b></a>", "<a/><b/>"} {
		t.Run(xml, func(t *testing.T) {
			_, _, router := newWiredDraftRouter(t)

			w := do(router, adminReq(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft",
				map[string]any{"bpmn_xml": xml, "record_version": 3}))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			problem := decodeProblem(t, w)
			assert.Equal(t, "INVALID_BPMN_XML", problem["code"])
			assert.Equal(t, "https://api.workflow.platform/errors/bad-request", problem["type"])
		})
	}
}
