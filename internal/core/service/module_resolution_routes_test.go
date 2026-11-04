package service

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestModuleWalk_ACycleBetweenModulesKeepsEveryRouteToEach(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	w := &moduleWalk{
		reachedBy: map[uuid.UUID][]moduleEdge{
			a: {{from: uuid.Nil, call: "c1", label: "A"}, {from: b, call: "toA", label: "A"}},
			b: {{from: uuid.Nil, call: "c2", label: "B"}, {from: a, call: "toB", label: "B"}},
		},
		docIssues: map[uuid.UUID][]domain.BPMNValidationError{
			a: {{Message: "in A"}},
			b: {{Message: "in B"}},
		},
		issueOrder: []uuid.UUID{a, b},
	}

	w.attributeModuleIssues()

	named := map[string][]string{}
	for _, e := range w.issues {
		named[e.Message[len(e.Message)-4:]] = append(named[e.Message[len(e.Message)-4:]], e.NodeID)
	}
	for _, msg := range []string{"in A", "in B"} {
		slices.Sort(named[msg])
		if !slices.Equal(named[msg], []string{"c1", "c2"}) {
			t.Errorf("%q is reported on %v, want both calls c1 and c2", msg, named[msg])
		}
	}
}
