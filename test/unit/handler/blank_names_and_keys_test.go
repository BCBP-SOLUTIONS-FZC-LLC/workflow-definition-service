package handler_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

var blankValues = []string{" ", "     ", "\t", "\n", " \t\r\n ", " ", " "}

func blankCheckedFields() map[string][]string {
	return map[string][]string{
		"create workflow":               {"key", "name"},
		"clone version":                 {"new_key", "new_name"},
		"create module":                 {"name"},
		"create starter":                {"name"},
		"create starter from a version": {"name"},
	}
}

func TestNamesAndKeys_MadeOnlyOfWhitespace_Return422BeforeTheServiceRuns(t *testing.T) {
	t.Parallel()

	for _, route := range nulRoutes() {
		for _, field := range blankCheckedFields()[route.name] {
			for _, value := range blankValues {
				t.Run(route.name+"/"+field+"/"+value, func(t *testing.T) {
					t.Parallel()

					var reached bool
					w := do(newNulRouterEngine(&reached), platformOperatorReq(route.method, route.path, withField(route.body, field, value)))

					require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
					assert.False(t, reached, "a blank value must not reach the service")
					p := decodeNulProblem(t, w.Body.Bytes())
					assert.Equal(t, "INVALID_INPUT", p.Code)
					assert.Equal(t, "request fields must not be blank", p.Detail)
					require.Len(t, p.InvalidParams, 1)
					assert.Equal(t, field, p.InvalidParams[0].Name)
					assert.Equal(t, "must not be blank", p.InvalidParams[0].Reason)
				})
			}
		}
	}
}

func TestNamesAndKeys_EveryBlankFieldIsListed(t *testing.T) {
	t.Parallel()

	var reached bool
	body := withField(withField(nulRoutes()[0].body, "key", " "), "name", "\t")

	w := do(newNulRouterEngine(&reached), platformOperatorReq(http.MethodPost, "/api/v1/workflows", body))

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	assert.False(t, reached)
	p := decodeNulProblem(t, w.Body.Bytes())
	require.Len(t, p.InvalidParams, 2)
	assert.Equal(t, "key", p.InvalidParams[0].Name)
	assert.Equal(t, "name", p.InvalidParams[1].Name)
}

func TestNamesAndKeys_BlankAndTooLongAreReportedTogether(t *testing.T) {
	t.Parallel()

	var reached bool
	body := withField(withField(nulRoutes()[0].body, "key", " "), "name", strings.Repeat("n", 256))

	w := do(newNulRouterEngine(&reached), platformOperatorReq(http.MethodPost, "/api/v1/workflows", body))

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	assert.False(t, reached)
	p := decodeNulProblem(t, w.Body.Bytes())
	assert.Equal(t, "INVALID_INPUT", p.Code)
	assert.Equal(t, "request fields are blank, exceed maximum length or contain NUL characters", p.Detail)
	require.Len(t, p.InvalidParams, 2)
	assert.Equal(t, "must not be blank", p.InvalidParams[0].Reason)
	assert.Equal(t, "must not exceed 255 characters", p.InvalidParams[1].Reason)
}

func TestOtherTextFields_MayBeBlank(t *testing.T) {
	t.Parallel()

	blankAllowed := map[string][]string{
		"create workflow":               {"description"},
		"clone version":                 {"new_description"},
		"create module":                 {"description"},
		"create starter":                {"description", "category"},
		"create starter from a version": {"description", "category"},
	}
	for _, route := range nulRoutes() {
		for _, field := range blankAllowed[route.name] {
			t.Run(route.name+"/"+field, func(t *testing.T) {
				t.Parallel()

				var reached bool
				w := do(newNulRouterEngine(&reached), platformOperatorReq(route.method, route.path, withField(route.body, field, "   ")))

				assert.True(t, reached, w.Body.String())
				assert.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
			})
		}
	}
}

func TestNamesAndKeys_WithSpacesAroundText_AreStoredAsSent(t *testing.T) {
	t.Parallel()

	var gotKey, gotName string
	h := newHandler(&fakeWorkflowSvc{
		create: func(_ context.Context, _, _ uuid.UUID, key, name, _, _ string) (*domain.Workflow, *domain.WorkflowVersion, error) {
			gotKey, gotName = key, name
			return nil, nil, domain.ErrUpstreamUnavailable
		},
	}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	body := map[string]any{"key": "  my-key ", "name": "  My name  ", "bpmn_xml": "<x/>"}

	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/workflows", body))

	assert.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	assert.Equal(t, "  my-key ", gotKey)
	assert.Equal(t, "  My name  ", gotName)
}
