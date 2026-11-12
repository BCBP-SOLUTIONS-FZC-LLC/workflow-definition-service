package http

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAsyncAPIPage_MarkupInTitleDescriptionHostAndPropertyNames_IsEscaped(t *testing.T) {
	body := asyncPageOK(t, `asyncapi: 3.0.0
info:
  title: 'w11ctitle<i>mark</i>'
  version: 1.0.0
  description: 'w11cinfo <script>w11cinfoscript()</script> & more'
servers:
  'w11csrv<b>':
    host: 'w11chost"><img src=w11cimg>'
    protocol: https
    description: 'w11cserverdesc <u>under</u>'
components:
  schemas:
    Holder:
      type: object
      properties:
        'w11cprop<em>x</em>':
          type: string
          description: 'w11cpropdesc <script>w11cpropscript()</script>'
`)

	for _, raw := range []string{
		`w11ctitle<i>`, `<script>w11cinfoscript()`, `<script>w11cpropscript()`, `<img src=w11cimg>`, `w11csrv<b>`, `<u>under</u>`, `w11cprop<em>`,
	} {
		assert.NotContains(t, body, raw, "markup %q from the spec must not reach the page as markup", raw)
	}

	assert.Contains(t, body, `<h1>w11ctitle&lt;i&gt;mark&lt;/i&gt;</h1>`)
	assert.Contains(t, body, `<div class="info-desc">w11cinfo &lt;script&gt;w11cinfoscript()&lt;/script&gt; &amp; more</div>`)
	assert.Contains(t, body, `<span class="server-name">w11csrv&lt;b&gt;</span>`)
	assert.Contains(t, body, `<span class="server-host">w11chost&#34;&gt;&lt;img src=w11cimg&gt;</span>`)
	assert.Contains(t, body, `<div class="server-desc">w11cserverdesc &lt;u&gt;under&lt;/u&gt;</div>`)

	row := asyncRow(t, body, "Holder", "w11cprop&lt;em&gt;x&lt;/em&gt;")
	assert.Contains(t, row, `<span class="prop-name">w11cprop&lt;em&gt;x&lt;/em&gt;</span>`)
	assert.Contains(t, row, `<span class="prop-desc">w11cpropdesc &lt;script&gt;w11cpropscript()&lt;/script&gt;</span>`)
}
