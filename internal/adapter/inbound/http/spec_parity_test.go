package http

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
)

const (
	internalPrefix  = "/api/v1/internal"
	publicSpecPath  = "../../../../docs/swagger/openapi.yaml"
	internalSpec    = "../../../../api/internal-openapi.yaml"
	asyncAPISpec    = "../../../../api/asyncapi.yaml"
	pathParamSyntax = `\{([^}]+)\}`
)

func specOperations(t *testing.T, path string) []string {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile(path)
	require.NoError(t, err)
	param := regexp.MustCompile(pathParamSyntax)
	var ops []string
	for p, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			ops = append(ops, method+" "+param.ReplaceAllString(p, ":$1"))
		}
	}
	require.NotEmpty(t, ops)
	sort.Strings(ops)
	return ops
}

func routerOperations(t *testing.T, internal bool) []string {
	t.Helper()
	engine := testRouter(fakeDBPinger{health: DBHealth{Healthy: true}}, fakePinger{})
	var ops []string
	for _, r := range engine.Routes() {
		isInternal := strings.HasPrefix(r.Path, internalPrefix)
		if isInternal != internal {
			continue
		}
		ops = append(ops, r.Method+" "+r.Path)
	}
	sort.Strings(ops)
	return ops
}

func TestPublicOpenAPIMatchesRoutes(t *testing.T) {
	assert.Equal(t, specOperations(t, publicSpecPath), routerOperations(t, false),
		"docs/swagger/openapi.yaml and the public routes must list the same operations")
}

func TestInternalOpenAPIMatchesRoutes(t *testing.T) {
	spec := specOperations(t, internalSpec)
	prefixed := make([]string, len(spec))
	for i, op := range spec {
		method, path, _ := strings.Cut(op, " ")
		if !strings.HasPrefix(path, internalPrefix) {
			path = internalPrefix + path
		}
		prefixed[i] = method + " " + path
	}
	sort.Strings(prefixed)
	assert.Equal(t, prefixed, routerOperations(t, true),
		"api/internal-openapi.yaml and the internal routes must list the same operations")
}

func TestAsyncAPIMessagesMatchHandledEvents(t *testing.T) {
	raw, err := readFile(asyncAPISpec)
	require.NoError(t, err)
	var doc struct {
		Components struct {
			Messages map[string]struct {
				Name string `yaml:"name"`
			} `yaml:"messages"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))

	var documented []string
	for _, m := range doc.Components.Messages {
		documented = append(documented, m.Name)
	}
	sort.Strings(documented)

	handled := append([]string(nil), handler.HandledEventTypes()...)
	sort.Strings(handled)
	assert.Equal(t, handled, documented, "api/asyncapi.yaml must document exactly the event types the inbound handler accepts")
}

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }
