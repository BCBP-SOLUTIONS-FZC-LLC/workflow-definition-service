package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
)

type asyncInfraProblem struct {
	Type     string `json:"type"`
	Status   int    `json:"status"`
	Code     string `json:"code"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

// asyncInfraWriteSpec puts spec at api/asyncapi.yaml under a fresh working directory.
func asyncInfraWriteSpec(t *testing.T, dir, spec string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "api"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "api", "asyncapi.yaml"), []byte(spec), 0o600))
}

func asyncInfraDevEngine(t *testing.T, spec string) *gin.Engine {
	t.Helper()
	dir := t.TempDir()
	asyncInfraWriteSpec(t, dir, spec)
	t.Chdir(dir)
	gin.SetMode(gin.TestMode)
	return NewRouter(RouterConfig{
		GinConfig: testGinConfig,
		AppEnv:    "dev",
		Handler:   handler.New(handler.Services{}),
		DB:        fakeDBPinger{},
		Cache:     fakePinger{},
	}).engine
}

func TestAsyncAPIRoute_DevMode_ServesTheHTMLPageWithoutAnyIdentity(t *testing.T) {
	engine := asyncInfraDevEngine(t, "asyncapi: 3.0.0\ninfo:\n  title: Zebra Events\n  version: 7.1.0\n")
	w := httptest.NewRecorder()

	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/asyncapi", nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	body := w.Body.String()
	assert.Contains(t, body, "<title>Zebra Events — AsyncAPI</title>")
	assert.Contains(t, body, `<span class="badge badge-version">v7.1.0</span>`)
	assert.Contains(t, body, "AsyncAPI 3.0.0</span>")
}

func TestAsyncAPIHandler_UnparsableSpecFile_Returns500ProblemWithoutTheCause(t *testing.T) {
	cases := []struct {
		name string
		spec string
	}{
		{name: "unterminated flow sequence", spec: "a: [\n"},
		{name: "messages given as a scalar", spec: asyncMinimalHead + "components:\n  messages: just-text\n"},
		{name: "info given as a list", spec: "asyncapi: 3.0.0\ninfo:\n  - title\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := asyncPageServe(t, tc.spec)

			require.Equal(t, http.StatusInternalServerError, code)
			var p asyncInfraProblem
			require.NoError(t, json.Unmarshal([]byte(body), &p), body)
			assert.Equal(t, "INTERNAL_ERROR", p.Code)
			assert.Equal(t, "https://api.workflow.platform/errors/internal-error", p.Type)
			assert.Equal(t, "the AsyncAPI document is unavailable", p.Detail)
			assert.NotContains(t, body, "yaml")
			assert.NotContains(t, body, "unmarshal")
			assert.NotContains(t, body, "line ")
			assert.NotContains(t, body, "<html")
		})
	}
}

func TestAsyncAPIHandler_SpecEditedBetweenRequests_SecondPageReflectsTheEdit(t *testing.T) {
	dir := t.TempDir()
	asyncInfraWriteSpec(t, dir, "asyncapi: 3.0.0\ninfo:\n  title: First Title\n  version: 1.0.0\n")
	t.Chdir(dir)

	c1, w1 := newTestContext()
	NewAsyncAPIHandler("")(c1)
	require.Equal(t, http.StatusOK, w1.Code)
	assert.Contains(t, w1.Body.String(), "<title>First Title — AsyncAPI</title>")

	asyncInfraWriteSpec(t, dir, "asyncapi: 3.0.0\ninfo:\n  title: Second Title\n  version: 1.0.0\n")
	c2, w2 := newTestContext()
	NewAsyncAPIHandler("")(c2)

	require.Equal(t, http.StatusOK, w2.Code)
	assert.Contains(t, w2.Body.String(), "<title>Second Title — AsyncAPI</title>")
	assert.NotContains(t, w2.Body.String(), "First Title")
}

func TestAsyncAPIHandler_MessageTitleAndSummary_FallBackToKeyAndDescription(t *testing.T) {
	body := asyncPageOK(t, asyncMinimalHead+`components:
  messages:
    NoTitle:
      summary: just a summary
    WithTitle:
      title: Friendly Title
    BothGiven:
      summary: the summary wins
      description: the description loses
    DescriptionOnly:
      description: "  only a description  "
    BlankSummary:
      summary: "   "
      description: fallback text
    Neither: {}
`)

	cases := []struct {
		name        string
		wantTitle   string
		wantSummary string
	}{
		{name: "NoTitle", wantTitle: `<span class="card-title">NoTitle</span>`, wantSummary: "just a summary"},
		{name: "WithTitle", wantTitle: `<span class="card-title">Friendly Title</span>`},
		{name: "BothGiven", wantTitle: `<span class="card-title">BothGiven</span>`, wantSummary: "the summary wins"},
		{name: "DescriptionOnly", wantTitle: `<span class="card-title">DescriptionOnly</span>`, wantSummary: "only a description"},
		{name: "BlankSummary", wantTitle: `<span class="card-title">BlankSummary</span>`, wantSummary: "fallback text"},
		{name: "Neither", wantTitle: `<span class="card-title">Neither</span>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := asyncMessageCard(t, body, tc.name)

			assert.Contains(t, card, tc.wantTitle)
			if tc.wantSummary == "" {
				assert.NotContains(t, card, `class="card-summary"`)
				return
			}
			assert.Contains(t, card, `<p class="card-summary" style="margin-bottom:.75rem">`+tc.wantSummary+`</p>`)
			assert.NotContains(t, card, "the description loses")
		})
	}
}

func TestAsyncAPIHandler_EventTypeLookup_ShowsTagOnlyWhenAValueIsFound(t *testing.T) {
	body := asyncPageOK(t, asyncMinimalHead+`components:
  messages:
    FullInbound:
      bindings:
        sns:
          messageAttributes:
            event_type:
              value: FullHappened
    ScalarBindingsInbound:
      bindings: nope
    ListBindingsInbound:
      bindings: [sns]
    NoMessageAttributesInbound:
      bindings:
        sns:
          other: x
    NoValueInbound:
      bindings:
        sns:
          messageAttributes:
            event_type:
              type: String
    ScalarEventTypeInbound:
      bindings:
        sns:
          messageAttributes:
            event_type: Direct
    NoBindingsInbound: {}
`)

	cases := []struct {
		name string
		want string
	}{
		{name: "FullInbound", want: `<span class="sns-attr">event_type: FullHappened</span>`},
		{name: "ScalarBindingsInbound"},
		{name: "ListBindingsInbound"},
		{name: "NoMessageAttributesInbound"},
		{name: "NoValueInbound"},
		{name: "ScalarEventTypeInbound"},
		{name: "NoBindingsInbound"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := asyncMessageCard(t, body, tc.name)

			if tc.want == "" {
				assert.NotContains(t, card, "sns-attr")
				assert.NotContains(t, card, "event_type")
				return
			}
			assert.Contains(t, card, tc.want)
		})
	}
}

func TestAsyncAPIHandler_SchemaCards_ShowNoPropertiesOrTheResolvedTarget(t *testing.T) {
	body := asyncPageOK(t, asyncMinimalHead+`components:
  messages:
    EmptyPayloadInbound:
      payload:
        $ref: '#/components/schemas/Hollow'
  schemas:
    Hollow:
      type: object
    Target:
      type: object
      required: [tid]
      properties:
        tid: {type: string}
    Alias:
      $ref: '#/components/schemas/Target'
    Dangling:
      $ref: '#/components/schemas/Missing'
`)

	const noProps = `<p style="color:var(--muted);font-size:.8rem">No properties.</p>`
	assert.Contains(t, asyncSchemaCard(t, body, "Hollow"), noProps)
	assert.Contains(t, asyncMessageCard(t, body, "EmptyPayloadInbound"), noProps)
	assert.Contains(t, asyncMessageCard(t, body, "EmptyPayloadInbound"), `Payload: <span style="color:var(--code);font-family:monospace">Hollow</span>`)

	alias := asyncSchemaCard(t, body, "Alias")
	assert.NotContains(t, alias, noProps)
	assert.Contains(t, alias, `<tr id="field--Alias--tid">`)
	assert.Contains(t, asyncRow(t, alias, "Alias", "tid"), `<span class="prop-req">required</span>`)

	dangling := asyncSchemaCard(t, body, "Dangling")
	assert.Contains(t, dangling, noProps)
	assert.NotContains(t, dangling, "tid")

	assert.Contains(t, asyncSchemaCard(t, body, "Target"), `<tr id="field--Target--tid">`)
}

func TestAsyncAPIHandler_Sections_AreSortedAndEmptyMapsGiveEmptySections(t *testing.T) {
	t.Run("messages and schemas sorted without an envelope schema", func(t *testing.T) {
		body := asyncPageOK(t, asyncMinimalHead+`components:
  messages:
    Mango: {}
    Apple: {}
    Cherry: {}
  schemas:
    Yak: {type: object}
    Bee: {type: object}
`)

		messages := asyncSection(t, body, `<div class="section" id="messages">`, `<div class="section" id="schemas">`)
		assert.Equal(t, 3, strings.Count(messages, `<div class="card" id="msg-`))
		assert.Less(t, strings.Index(messages, `id="msg-Apple"`), strings.Index(messages, `id="msg-Cherry"`))
		assert.Less(t, strings.Index(messages, `id="msg-Cherry"`), strings.Index(messages, `id="msg-Mango"`))

		schemas := asyncSection(t, body, `<div class="section" id="schemas">`, `</main>`)
		assert.Equal(t, 2, strings.Count(schemas, `<div class="card" id="schema-`))
		assert.Less(t, strings.Index(schemas, `id="schema-Bee"`), strings.Index(schemas, `id="schema-Yak"`))

		sidebarMessages := asyncSection(t, body, `<div class="sidebar-label">Messages</div>`, `<div class="sidebar-label">Schemas</div>`)
		assert.Less(t, strings.Index(sidebarMessages, `href="#msg-Apple"`), strings.Index(sidebarMessages, `href="#msg-Mango"`))
	})

	t.Run("no components renders empty sections", func(t *testing.T) {
		body := asyncPageOK(t, asyncMinimalHead)

		assert.Contains(t, body, `<div class="section" id="messages"><div class="section-title">Messages</div></div>`)
		assert.Contains(t, body, `<div class="section" id="schemas"><div class="section-title">Schemas</div></div>`)
		assert.NotContains(t, body, `class="card"`)
	})
}

func TestAsyncAPIHandler_ServerEntries_RenderEachFieldAndCutLongDescriptions(t *testing.T) {
	long := strings.Repeat("d", 250)
	body := asyncPageOK(t, asyncMinimalHead+`servers:
  first:
    host: one.example.internal
    protocol: https
    description: `+long+`
    variables: ignored-extra-field
  second:
    host: two.example.internal
    protocol: sqs
    description: short note
`)

	section := asyncSection(t, body, `<div class="section" id="servers">`, `<div class="section" id="messages">`)
	assert.Equal(t, 2, strings.Count(section, `class="server-item"`))
	assert.Contains(t, section, `<div class="server-desc">`+strings.Repeat("d", 200)+`…</div>`)
	assert.NotContains(t, section, strings.Repeat("d", 201))
	assert.Contains(t, section, `<span class="server-proto">HTTPS</span> <span class="server-host">one.example.internal</span>`)
	assert.Contains(t, section, `<span class="server-proto">SQS</span> <span class="server-host">two.example.internal</span>`)
	assert.Contains(t, section, `<div class="server-desc">short note</div>`)
	assert.NotContains(t, section, "ignored-extra-field")
	assert.Less(t, strings.Index(section, "one.example.internal"), strings.Index(section, "two.example.internal"), "servers keep spec order")
}

func TestAsyncAPIHandler_PropertyDescription_IsTrimmedAndCutAt200Bytes(t *testing.T) {
	long := strings.Repeat("x", 300)
	body := asyncPageOK(t, asyncMinimalHead+`components:
  schemas:
    Described:
      type: object
      properties:
        longText:
          type: string
          description: `+long+`
        padded:
          type: string
          description: "   fits as is   "
        exact:
          type: string
          description: `+strings.Repeat("y", 200)+`
        bare:
          type: array
`)

	assert.Contains(t, asyncRow(t, body, "Described", "longText"), `<span class="prop-desc">`+strings.Repeat("x", 200)+`…</span>`)
	assert.NotContains(t, asyncRow(t, body, "Described", "longText"), strings.Repeat("x", 201))
	assert.Contains(t, asyncRow(t, body, "Described", "padded"), `<span class="prop-desc">fits as is</span>`)
	exact := asyncRow(t, body, "Described", "exact")
	assert.Contains(t, exact, `<span class="prop-desc">`+strings.Repeat("y", 200)+`</span>`, "exactly 200 bytes is not cut")
	assert.NotContains(t, exact, "…")
	assert.Contains(t, asyncRow(t, body, "Described", "bare"), `<span class="prop-type">array</span>`, "an array with no item type or ref shows plain array")
}
