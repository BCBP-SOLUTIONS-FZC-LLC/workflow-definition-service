package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestValidateBPMN_WhitespaceOnlyXML_Returns200InvalidWithMissingNamespace(t *testing.T) {
	t.Parallel()

	const (
		nsBPMN  = "http://www.omg.org/spec/BPMN/20100524/MODEL"
		nsZeebe = "http://camunda.org/schema/zeebe/1.0"
	)
	cases := []struct {
		name string
		xml  string
	}{
		{"spaces", "   "},
		{"single space", " "},
		{"newline and tab", "\n\t"},
		{"mixed whitespace", " \n\t \r\n "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			validation := service.NewValidationService(service.ValidationDeps{
				Compiler:       bpmn_compiler.New(),
				ModuleVersions: mocks.NewMockModuleVersionRepository(ctrl),
			})
			router := newRouter(handler.New(handler.Services{Validation: validation}))

			w := do(router, req(http.MethodPost, "/api/v1/workflows/validate", map[string]any{"bpmn_xml": tc.xml}))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var body struct {
				IsValid bool `json:"is_valid"`
				Issues  []struct {
					NodeID   string `json:"node_id"`
					Code     string `json:"code"`
					Message  string `json:"message"`
					Severity string `json:"severity"`
				} `json:"issues"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.False(t, body.IsValid)
			require.Len(t, body.Issues, 1)
			assert.Equal(t, "MISSING_NAMESPACE", body.Issues[0].Code)
			assert.Equal(t, "error", body.Issues[0].Severity)
			assert.Empty(t, body.Issues[0].NodeID)
			assert.Contains(t, body.Issues[0].Message, nsBPMN)
			assert.NotContains(t, body.Issues[0].Message, nsZeebe)
		})
	}
}
