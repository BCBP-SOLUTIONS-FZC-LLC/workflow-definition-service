package http

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
)

func TestAsyncAPIPage_EnvironmentBadge_ComesFromTheConfiguredEnvironmentNotTheENVVariable(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "api"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "api", "asyncapi.yaml"), []byte(asyncMinimalHead), 0o600))
	t.Chdir(dir)
	t.Setenv("ENV", "prod")
	gin.SetMode(gin.TestMode)
	engine := NewRouter(RouterConfig{
		GinConfig: testGinConfig,
		AppEnv:    "dev",
		Handler:   handler.New(handler.Services{}),
		DB:        fakeDBPinger{}, Cache: fakePinger{},
	}).engine

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/asyncapi", nil))

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `margin-left:auto">DEV</span>`)
	assert.NotContains(t, w.Body.String(), `margin-left:auto">PROD</span>`)
}

func TestAsyncAPIPage_LongText_IsCutByCharactersAndNeverSplitsOne(t *testing.T) {
	body := asyncPageOK(t, `asyncapi: 3.0.0
info:
  title: Test
  version: 1.0.0
  description: `+strings.Repeat("é", 900)+`
servers:
  first:
    host: one.example.internal
    protocol: https
    description: `+strings.Repeat("é", 250)+`
components:
  schemas:
    Described:
      type: object
      properties:
        longText:
          type: string
          description: `+strings.Repeat("é", 250)+`
        shortText:
          type: string
          description: `+strings.Repeat("é", 150)+`
`)

	assert.True(t, utf8.ValidString(body), "a cut must not split a multi-byte character")
	assert.Contains(t, body, `<div class="info-desc">`+strings.Repeat("é", 800)+"\n\n[truncated")
	assert.NotContains(t, body, strings.Repeat("é", 801))
	assert.Contains(t, body, `<div class="server-desc">`+strings.Repeat("é", 200)+`…</div>`)
	assert.Contains(t, asyncRow(t, body, "Described", "longText"), `<span class="prop-desc">`+strings.Repeat("é", 200)+`…</span>`)
	assert.Contains(t, asyncRow(t, body, "Described", "shortText"), `<span class="prop-desc">`+strings.Repeat("é", 150)+`</span>`, "150 characters (300 bytes) is under the limit")
}

func TestAsyncAPIPage_ShortInfoDescriptionWithManyBytes_IsNotCut(t *testing.T) {
	body := asyncPageOK(t, `asyncapi: 3.0.0
info:
  title: Test
  version: 1.0.0
  description: `+strings.Repeat("é", 500)+`
`)

	assert.Contains(t, body, `<div class="info-desc">`+strings.Repeat("é", 500)+`</div>`)
	assert.NotContains(t, body, "[truncated")
}

func TestAsyncAPIPage_SchemaAndMessageNames_AreEscapedInIdAndHrefAttributes(t *testing.T) {
	body := asyncPageOK(t, asyncMinimalHead+`components:
  messages:
    'm"><b>x':
      title: bad message
  schemas:
    'a"><script>x</script>':
      type: object
      properties:
        n:
          type: string
`)

	assert.NotContains(t, body, `<script>x</script>`)
	assert.NotContains(t, body, `<b>x`)
	assert.Contains(t, body, `id="schema-a&#34;&gt;&lt;script&gt;x&lt;/script&gt;"`)
	assert.Contains(t, body, `<a href="#schema-a&#34;&gt;&lt;script&gt;x&lt;/script&gt;">`)
	assert.Contains(t, body, `id="msg-m&#34;&gt;&lt;b&gt;x"`)
	assert.Contains(t, body, `<a href="#msg-m&#34;&gt;&lt;b&gt;x">`)
}

func TestAsyncAPIPage_SchemaReferenceLoops_RenderInsteadOfOverflowingTheStack(t *testing.T) {
	cases := []struct {
		name string
		spec string
	}{
		{
			name: "two schemas referring to each other",
			spec: `  schemas:
    A:
      $ref: '#/components/schemas/B'
    B:
      $ref: '#/components/schemas/A'
`,
		},
		{
			name: "a schema that is its own ref",
			spec: `  schemas:
    A:
      $ref: '#/components/schemas/A'
`,
		},
		{
			name: "allOf members that lead back to the schema",
			spec: `  schemas:
    A:
      allOf:
        - $ref: '#/components/schemas/B'
        - type: object
          properties:
            own:
              type: string
    B:
      allOf:
        - $ref: '#/components/schemas/A'
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := asyncPageOK(t, asyncMinimalHead+"components:\n"+tc.spec)

			assert.Contains(t, body, `id="schema-A"`)
		})
	}
}

func TestAsyncAPIPage_SchemaDiamondThatIsNotALoop_StillMergesBothPaths(t *testing.T) {
	body := asyncPageOK(t, asyncMinimalHead+`components:
  schemas:
    Base:
      type: object
      properties:
        id:
          type: string
    Left:
      allOf:
        - $ref: '#/components/schemas/Base'
    Right:
      allOf:
        - $ref: '#/components/schemas/Base'
    Both:
      allOf:
        - $ref: '#/components/schemas/Left'
        - $ref: '#/components/schemas/Right'
`)

	assert.Contains(t, asyncSchemaCard(t, body, "Both"), `<span class="prop-name">id</span>`)
}

func TestNewAsyncAPIHandler_ShowsTheEnvironmentItIsGiven(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "api"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "api", "asyncapi.yaml"), []byte(asyncMinimalHead), 0o600))
	t.Chdir(dir)
	c, w := newTestContext()

	NewAsyncAPIHandler("staging")(c)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `background:#f59e0b;color:#fff;margin-left:auto">STAGING</span>`)
}
