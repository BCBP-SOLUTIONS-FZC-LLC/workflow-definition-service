package http

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAsyncAPIPage_PropertyNameWithApostrophe_IsNotPlacedInsideAScriptString(t *testing.T) {
	body := asyncPageOK(t, asyncMinimalHead+`components:
  schemas:
    Holder:
      type: object
      properties:
        "it's;alert(1)":
          type: string
`)

	row := asyncRow(t, body, "Holder", "it&#39;s;alert(1)")

	assert.NotContains(t, row, "copyText('", "the name must not sit inside a script string literal")
	assert.Contains(t, row, `data-copy="it&#39;s;alert(1)"`, "the name travels as an escaped attribute value")
	assert.Contains(t, row, `onclick="copyText(this.dataset.copy)"`)
}
