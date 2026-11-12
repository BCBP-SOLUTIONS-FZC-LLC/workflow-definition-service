package bpmn_compiler

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeclaredNamespaces_ReportsTheRootSpaceAndTheZeebeDeclaration(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		xml       string
		wantRoot  string
		wantZeebe bool
	}{
		{"both declared on the root", `<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `"><bpmn:process id="p"/></bpmn:definitions>`, nsBPMN, true},
		{"zeebe declared as the default namespace of a child", `<bpmn:definitions xmlns:bpmn="` + nsBPMN + `"><bpmn:process id="p"><x xmlns="` + nsZeebe + `"/></bpmn:process></bpmn:definitions>`, nsBPMN, true},
		{"zeebe declared deep in the document", `<bpmn:definitions xmlns:bpmn="` + nsBPMN + `"><bpmn:process id="p"><bpmn:extensionElements xmlns:z="` + nsZeebe + `"/></bpmn:process></bpmn:definitions>`, nsBPMN, true},
		{"no zeebe declaration", `<bpmn:definitions xmlns:bpmn="` + nsBPMN + `"><bpmn:process id="p"/></bpmn:definitions>`, nsBPMN, false},
		{"another namespace with the zeebe prefix", `<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="urn:other"/>`, nsBPMN, false},
		{"root in another namespace", `<definitions xmlns="urn:other" xmlns:zeebe="` + nsZeebe + `"/>`, "urn:other", true},
		{"not xml at all", `no markup`, "", false},
		{"a syntax error after the declaration does not undo it", `<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `"><bpmn:process`, nsBPMN, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, zeebe := declaredNamespaces(tc.xml)

			assert.Equal(t, tc.wantRoot, root)
			assert.Equal(t, tc.wantZeebe, zeebe)
		})
	}
}

func TestDeclaredNamespaces_StopsAtTheDeclarationInsteadOfReadingTheWholeDocument(t *testing.T) {
	doc := func(children int) string {
		var b strings.Builder
		b.WriteString(`<bpmn:definitions xmlns:bpmn="` + nsBPMN + `" xmlns:zeebe="` + nsZeebe + `"><bpmn:process id="p">`)
		for i := range children {
			fmt.Fprintf(&b, `<bpmn:userTask id="t%d" name="task %d"/>`, i, i)
		}
		b.WriteString(`</bpmn:process></bpmn:definitions>`)
		return b.String()
	}
	small, large := doc(10), doc(5000)
	run := func(d string) float64 {
		return testing.AllocsPerRun(5, func() { declaredNamespaces(d) })
	}

	assert.InDelta(t, run(small), run(large), 5, "the work must not grow with what follows the declaration")
}
