package http

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func asyncPageServe(t *testing.T, specYAML string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "api"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "api", "asyncapi.yaml"), []byte(specYAML), 0o600))
	t.Chdir(dir)

	c, w := newTestContext()
	AsyncAPIHandler(c)
	return w.Code, w.Body.String()
}

func asyncPageOK(t *testing.T, specYAML string) string {
	t.Helper()
	code, body := asyncPageServe(t, specYAML)
	require.Equal(t, http.StatusOK, code, body)
	return body
}

func asyncSection(t *testing.T, body, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(body, startMarker)
	require.GreaterOrEqual(t, start, 0, "marker %q not found", startMarker)
	rest := body[start:]
	if end := strings.Index(rest[len(startMarker):], endMarker); end >= 0 {
		return rest[:len(startMarker)+end]
	}
	return rest
}

func asyncRow(t *testing.T, body, schema, field string) string {
	t.Helper()
	return asyncSection(t, body, `<tr id="field--`+schema+`--`+field+`">`, `</tr>`)
}

func asyncMessageCard(t *testing.T, body, name string) string {
	t.Helper()
	return asyncSection(t, body, `<div class="card" id="msg-`+name+`">`, `<div class="card"`)
}

func asyncSchemaCard(t *testing.T, body, name string) string {
	t.Helper()
	return asyncSection(t, body, `<div class="card" id="schema-`+name+`">`, `<div class="card"`)
}

const asyncMinimalHead = "asyncapi: 3.0.0\ninfo:\n  title: Test\n  version: 1.0.0\n"

func TestAsyncAPIHandler_UndecodableSchemaNodes_Return500Problem(t *testing.T) {
	cases := []struct {
		name string
		spec string
	}{
		{
			name: "property whose items is a scalar",
			spec: asyncMinimalHead + `components:
  schemas:
    Bad:
      type: object
      properties:
        tags:
          type: array
          items: foo
`,
		},
		{
			name: "schema whose properties is a list",
			spec: asyncMinimalHead + `components:
  schemas:
    Bad:
      type: object
      properties:
        - name
        - id
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := asyncPageServe(t, tc.spec)

			require.Equal(t, http.StatusInternalServerError, code)
			assert.Contains(t, body, `"code":"INTERNAL_ERROR"`)
			assert.NotContains(t, body, "<html", "no partial page is rendered")
		})
	}
}

func TestAsyncAPIHandler_PropertyTypeGivenAsAList_IsFlattened(t *testing.T) {
	body := asyncPageOK(t, asyncMinimalHead+`x-anchor: &shared string
components:
  schemas:
    Flat:
      type: object
      properties:
        nullable:
          type: [string, null]
        onlyNull:
          type: [null]
        union:
          type: [string, integer]
        mapped:
          type:
            a: b
        aliased:
          type: *shared
`)

	cases := []struct {
		field string
		want  string
	}{
		{field: "nullable", want: `<span class="prop-type">string</span>`},
		{field: "onlyNull", want: `<span class="prop-type"></span>`},
		{field: "union", want: `<span class="prop-type">string|integer</span>`},
		{field: "mapped", want: `<span class="prop-type"></span>`},
		{field: "aliased", want: `<span class="prop-type"></span>`},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			assert.Contains(t, asyncRow(t, body, "Flat", tc.field), tc.want)
		})
	}
}

func TestAsyncAPIHandler_PropertyRows_ListRequiredFirstThenDeclaredOrder(t *testing.T) {
	body := asyncPageOK(t, asyncMinimalHead+`components:
  schemas:
    Ordered:
      type: object
      required: [c]
      properties:
        b: {type: string}
        a: {type: string}
        c: {type: string}
`)

	card := asyncSchemaCard(t, body, "Ordered")
	posC := strings.Index(card, `id="field--Ordered--c"`)
	posB := strings.Index(card, `id="field--Ordered--b"`)
	posA := strings.Index(card, `id="field--Ordered--a"`)
	require.True(t, posC >= 0 && posB >= 0 && posA >= 0, card)
	assert.Less(t, posC, posB, "required property first")
	assert.Less(t, posB, posA, "the rest keep declared order, not alphabetical")
}

func TestRenderPropsTable_SchemaWithoutDeclaredOrder_FallsBackToAlphabetical(t *testing.T) {
	var buf bytes.Buffer
	renderPropsTable(&buf, &asyncSchema{Properties: map[string]asyncProp{
		"b": {Type: "string"}, "a": {Type: "string"}, "c": {Type: "string"},
	}}, "Plain")

	out := buf.String()
	posA := strings.Index(out, `id="field--Plain--a"`)
	posB := strings.Index(out, `id="field--Plain--b"`)
	posC := strings.Index(out, `id="field--Plain--c"`)
	require.True(t, posA >= 0 && posB >= 0 && posC >= 0, out)
	assert.Less(t, posA, posB)
	assert.Less(t, posB, posC)
}

func TestAsyncAPIHandler_AllOfSchema_MergesMembersAndIgnoresSiblingRef(t *testing.T) {
	body := asyncPageOK(t, asyncMinimalHead+`components:
  schemas:
    Other:
      type: object
      properties:
        zed: {type: string}
    Combined:
      $ref: '#/components/schemas/Other'
      allOf:
        - type: object
          required: [x]
          properties:
            x: {type: string}
        - type: object
          required: [y]
          properties:
            x: {type: integer}
            y: {type: string}
`)

	card := asyncSchemaCard(t, body, "Combined")
	rowX := asyncRow(t, card, "Combined", "x")
	assert.Contains(t, rowX, `<span class="prop-type">integer</span>`, "the later member wins for a shared property")
	assert.NotContains(t, rowX, `<span class="prop-type">string</span>`)
	assert.Contains(t, rowX, `prop-req">required`, "required lists from both members are kept")
	assert.Contains(t, asyncRow(t, card, "Combined", "y"), `prop-req">required`)
	assert.NotContains(t, card, "zed", "a $ref next to allOf is ignored")
}

func TestAsyncAPIHandler_Messages_RenderDirectionEventTypeAndPayload(t *testing.T) {
	body := asyncPageOK(t, asyncMinimalHead+`components:
  messages:
    ThingInbound:
      title: Thing in
      payload:
        $ref: '#/components/schemas/Thing'
      bindings:
        sns:
          messageAttributes:
            event_type:
              value: ThingHappened
    ThingOut:
      payload:
        $ref: '#/components/schemas/Thing'
    Ghost:
      payload:
        $ref: '#/components/schemas/Missing'
    Empty:
      summary: nothing attached
  schemas:
    Thing:
      type: object
      properties:
        id: {type: string}
`)

	cases := []struct {
		name         string
		wantBadge    string
		wantEvent    bool
		wantTable    bool
		wantPayloadL bool
	}{
		{name: "ThingInbound", wantBadge: `m-recv">RECEIVE`, wantEvent: true, wantTable: true, wantPayloadL: true},
		{name: "ThingOut", wantBadge: `m-send">SEND`, wantEvent: false, wantTable: true, wantPayloadL: true},
		{name: "Ghost", wantBadge: `m-send">SEND`, wantEvent: false, wantTable: false, wantPayloadL: false},
		{name: "Empty", wantBadge: `m-send">SEND`, wantEvent: false, wantTable: false, wantPayloadL: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := asyncMessageCard(t, body, tc.name)

			assert.Contains(t, card, tc.wantBadge)
			assert.Equal(t, tc.wantEvent, strings.Contains(card, `event_type: ThingHappened`))
			assert.Equal(t, tc.wantEvent, strings.Contains(card, `class="sns-attr"`))
			assert.Equal(t, tc.wantTable, strings.Contains(card, `class="prop-table"`))
			assert.Equal(t, tc.wantPayloadL, strings.Contains(card, `Payload: `))
		})
	}
}

func TestRenderPage_EnvironmentBadge_ColourAndLabelFollowTheEnvironmentName(t *testing.T) {
	cases := []struct {
		name      string
		env       string
		wantColor string
		wantLabel string
	}{
		{name: "empty is DEV green", env: "", wantColor: "#22c55e", wantLabel: "DEV"},
		{name: "prod is red", env: "prod", wantColor: "#ec4b3c", wantLabel: "PROD"},
		{name: "production is red", env: "Production", wantColor: "#ec4b3c", wantLabel: "PRODUCTION"},
		{name: "staging is amber", env: "staging", wantColor: "#f59e0b", wantLabel: "STAGING"},
		{name: "stage is amber", env: "stage", wantColor: "#f59e0b", wantLabel: "STAGE"},
		{name: "other value is green with its own label", env: "qa", wantColor: "#22c55e", wantLabel: "QA"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var spec asyncSpec
			require.NoError(t, yaml.Unmarshal([]byte(asyncMinimalHead), &spec))
			var buf bytes.Buffer

			renderPage(&buf, &spec, tc.env)

			assert.Contains(t, buf.String(),
				`<span class="badge" style="background:`+tc.wantColor+`;color:#fff;margin-left:auto">`+tc.wantLabel+`</span>`)
		})
	}
}

func TestAsyncAPIHandler_SpecWithoutServers_OmitsSectionButSortsSchemasEnvelopeFirst(t *testing.T) {
	body := asyncPageOK(t, asyncMinimalHead+`components:
  schemas:
    Zeta: {type: object}
    Alpha: {type: object}
    EventEnvelope: {type: object}
`)

	assert.NotContains(t, body, `id="servers"`, "no Servers section without a servers key")
	assert.Contains(t, body, `<a href="#servers">Servers</a>`, "the sidebar still links to it")

	posEnvelope := strings.Index(body, `id="schema-EventEnvelope"`)
	posAlpha := strings.Index(body, `id="schema-Alpha"`)
	posZeta := strings.Index(body, `id="schema-Zeta"`)
	require.True(t, posEnvelope >= 0 && posAlpha >= 0 && posZeta >= 0)
	assert.Less(t, posEnvelope, posAlpha)
	assert.Less(t, posAlpha, posZeta)
}

func TestAsyncAPIHandler_PropertyTypeCell_RendersLinksAndFormats(t *testing.T) {
	body := asyncPageOK(t, asyncMinimalHead+`components:
  schemas:
    Target: {type: object}
    Cells:
      type: object
      properties:
        direct:
          $ref: '#/components/schemas/Target'
        many:
          type: array
          items:
            $ref: '#/components/schemas/Target'
        names:
          type: array
          items:
            type: string
        ident:
          type: string
          format: uuid
`)

	cases := []struct {
		field string
		want  string
	}{
		{field: "direct", want: `<a href="#schema-Target" class="prop-type schema-link">Target</a>`},
		{field: "many", want: `<span class="prop-type">array&lt;<a href="#schema-Target" class="prop-type schema-link">Target</a>&gt;</span>`},
		{field: "names", want: `<span class="prop-type">array&lt;string&gt;</span>`},
		{field: "ident", want: `<span class="prop-type">string(uuid)</span>`},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			assert.Contains(t, asyncRow(t, body, "Cells", tc.field), tc.want)
		})
	}
}

func TestAsyncAPIHandler_PropertyRow_RendersEnumChipsValuesAndExample(t *testing.T) {
	body := asyncPageOK(t, asyncMinimalHead+`components:
  schemas:
    Rich:
      type: object
      required: [status]
      properties:
        status:
          type: string
          enum: [OPEN, CLOSED]
        tags:
          type: array
          items:
            type: string
            enum: [red, blue]
        meta:
          type: object
          example:
            a: 1
`)

	status := asyncRow(t, body, "Rich", "status")
	assert.Contains(t, status, `<span class="enum-val">OPEN</span><span class="enum-val">CLOSED</span>`)
	assert.Contains(t, status, `<span class="prop-req">required</span>`)

	tags := asyncRow(t, body, "Rich", "tags")
	assert.Contains(t, tags, `values:</div><div class="enum-list"><span class="enum-val">red</span><span class="enum-val">blue</span>`)
	assert.NotContains(t, tags, `prop-req`, "an optional property carries no required tag")

	meta := asyncRow(t, body, "Rich", "meta")
	assert.Contains(t, meta, "<pre ")
	assert.Contains(t, meta, "{\n  &#34;a&#34;: 1\n}", "the example is indented JSON")
}

func TestAsyncAPIHandler_ServersSection_RendersMappingAndEmptiesForList(t *testing.T) {
	cases := []struct {
		name  string
		spec  string
		check func(t *testing.T, section string)
	}{
		{
			name: "mapping",
			spec: asyncMinimalHead + `servers:
  queue:
    host: sqs.eu-west-1.amazonaws.com
    protocol: sqs
    description: "   events go here   "
`,
			check: func(t *testing.T, section string) {
				assert.Equal(t, 1, strings.Count(section, `class="server-item"`))
				assert.Contains(t, section, `<span class="server-name">queue</span>`)
				assert.Contains(t, section, `<span class="server-proto">SQS</span>`)
				assert.Contains(t, section, `<span class="server-host">sqs.eu-west-1.amazonaws.com</span>`)
				assert.Contains(t, section, `<div class="server-desc">events go here</div>`)
			},
		},
		{
			name: "list",
			spec: asyncMinimalHead + `servers:
  - host: example.com
    protocol: sqs
`,
			check: func(t *testing.T, section string) {
				assert.Contains(t, section, `class="section-title">Servers`)
				assert.NotContains(t, section, `server-item`, "a list yields an empty card")
				assert.NotContains(t, section, "example.com")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := asyncPageOK(t, tc.spec)

			tc.check(t, asyncSection(t, body, `<div class="section" id="servers">`, `<div class="section" id="messages">`))
		})
	}
}
