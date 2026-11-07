package bpmncore

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// compCoreRecordingHandler records each node it compiles, appends one step per node
// and follows every forward edge, like a minimal task handler.
type compCoreRecordingHandler struct {
	nodeType FlowNodeType
	compiled *[]string
}

func (h compCoreRecordingHandler) NodeType() FlowNodeType { return h.nodeType }

func (h compCoreRecordingHandler) Validate(
	string, *BPMNProcess, *Graph, *BPMNDefinitions,
	map[string]StageTypeHandler, map[FlowNodeType]ElementHandler,
) []domain.BPMNValidationError {
	return nil
}

func (h compCoreRecordingHandler) Compile(nodeID string, cs *CompileState) error {
	*h.compiled = append(*h.compiled, nodeID)
	cs.AppendStep(dsl.ExecutionStep{Sequential: []string{nodeID}})
	for _, next := range cs.ForwardNexts(nodeID) {
		if err := cs.TraverseNode(next); err != nil {
			return err
		}
	}
	return nil
}

func TestTraverseNode_NodeReachedAgainInTheSameState_IsCompiledOnce(t *testing.T) {
	cases := []struct {
		name         string
		outgoing     map[string][]string
		traverse     []string
		wantCompiled []string
	}{
		{
			name:         "same node traversed twice",
			outgoing:     map[string][]string{"t1": nil},
			traverse:     []string{"t1", "t1"},
			wantCompiled: []string{"t1"},
		},
		{
			name:         "cycle with no back edge terminates",
			outgoing:     map[string][]string{"a": {"b"}, "b": {"a"}},
			traverse:     []string{"a"},
			wantCompiled: []string{"a", "b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newMinimalState()
			var compiled []string
			s.Elements[NodeTypeUserTask] = compCoreRecordingHandler{nodeType: NodeTypeUserTask, compiled: &compiled}
			for from, tos := range tc.outgoing {
				s.G.Outgoing[from] = tos
				s.G.NodeType[from] = NodeTypeUserTask
				for _, to := range tos {
					s.G.NodeType[to] = NodeTypeUserTask
				}
			}

			for _, node := range tc.traverse {
				if err := s.TraverseNode(node); err != nil {
					t.Fatalf("TraverseNode(%q) error = %v, want nil", node, err)
				}
			}

			if !slices.Equal(compiled, tc.wantCompiled) {
				t.Errorf("compiled nodes = %v, want %v", compiled, tc.wantCompiled)
			}
			if got := s.StepsCount(); got != len(tc.wantCompiled) {
				t.Errorf("StepsCount() = %d, want %d", got, len(tc.wantCompiled))
			}
			if s.sd.nodes != len(tc.wantCompiled) {
				t.Errorf("node counter = %d, want %d: a repeat visit must not count against the budget", s.sd.nodes, len(tc.wantCompiled))
			}
		})
	}
}

func TestLazyJoins_Get_BuildsOnceAndSharesTheResultWithBranchStates(t *testing.T) {
	g := &Graph{
		Outgoing: map[string][]string{"a": {"b"}, "b": {"a"}},
		NodeType: map[string]FlowNodeType{},
	}
	firstBackEdges := map[[2]string]bool{{"b", "a"}: true}

	t.Run("later calls return the first result even with other back edges", func(t *testing.T) {
		l := &lazyJoins{}
		first := l.get(g, firstBackEdges)
		second := l.get(g, map[[2]string]bool{})

		if first != second {
			t.Fatal("get() built a second Joins; it must reuse the first")
		}
		if second.ReachesUnbrokenCycle("a") {
			t.Error("ReachesUnbrokenCycle(a) = true, want false: the joins were built with b->a as a back edge")
		}
	})

	t.Run("a branch state shares its parent's lazy joins", func(t *testing.T) {
		parent := newMinimalState()
		if parent.joins.joins != nil {
			t.Fatal("joins were built before the first get()")
		}
		branch := NewBranchState(parent, "stop")

		if branch.joins != parent.joins {
			t.Fatal("the branch state has its own lazyJoins, want the parent's")
		}
		fromBranch := branch.joins.get(g, firstBackEdges)
		fromParent := parent.joins.get(g, map[[2]string]bool{})
		if fromBranch != fromParent {
			t.Error("parent and branch got different Joins, want one shared value")
		}
	})
}

func TestProcessBackEdges_BoundaryEventsAreRootsOfTheSearch(t *testing.T) {
	proc := &BPMNProcess{BoundaryEvents: []BPMNBoundaryEvent{{ID: "B", AttachedToRef: "t1"}}}
	cases := []struct {
		name     string
		outgoing map[string][]string
		want     map[[2]string]bool
	}{
		{
			name: "loop reachable only from the boundary path",
			outgoing: map[string][]string{
				"S": {"t1"}, "t1": {"E"},
				"B": {"t2"}, "t2": {"t3"}, "t3": {"t2", "E"},
			},
			want: map[[2]string]bool{{"t3", "t2"}: true},
		},
		{
			name: "boundary path that rejoins the main flow has no back edge",
			outgoing: map[string][]string{
				"S": {"t1"}, "t1": {"t2"}, "t2": {"E"},
				"B": {"t2"},
			},
			want: map[[2]string]bool{},
		},
		{
			name: "boundary path into a main-flow loop does not reclassify the loop",
			outgoing: map[string][]string{
				"S": {"t1"}, "t1": {"t2"}, "t2": {"t1"},
				"B": {"t2"},
			},
			want: map[[2]string]bool{{"t2", "t1"}: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := &Graph{Outgoing: tc.outgoing}

			got := ProcessBackEdges(proc, g, "S")

			if !maps.Equal(got, tc.want) {
				t.Errorf("ProcessBackEdges() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseStringObject_InputForms(t *testing.T) {
	const errPrefix = "not a JSON object of strings: "
	cases := []struct {
		name    string
		source  string
		want    map[string]string
		wantErr string // "syntax" or "type": the kind of JSON error wrapped
	}{
		{name: "plain object", source: `{"a":"b"}`, want: map[string]string{"a": "b"}},
		{name: "leading equals sign", source: `={"a":"b"}`, want: map[string]string{"a": "b"}},
		{name: "equals sign after spaces", source: `  = {"a":"b"}  `, want: map[string]string{"a": "b"}},
		{name: "json null gives a nil map", source: `null`, want: nil},
		{name: "equals sign then null", source: `=null`, want: nil},
		{name: "empty string", source: ``, wantErr: "syntax"},
		{name: "only an equals sign", source: `=`, wantErr: "syntax"},
		{name: "only one equals sign is stripped", source: `=={"a":"b"}`, wantErr: "syntax"},
		{name: "non-string value", source: `{"a":1}`, wantErr: "type"},
		{name: "json array", source: `["a"]`, wantErr: "type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseStringObject(tc.source)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("parseStringObject(%q) error = %v, want nil", tc.source, err)
				}
				if (got == nil) != (tc.want == nil) || !maps.Equal(got, tc.want) {
					t.Errorf("parseStringObject(%q) = %#v, want %#v", tc.source, got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("parseStringObject(%q) = %v, want an error", tc.source, got)
			}
			if got != nil {
				t.Errorf("parseStringObject(%q) map = %v, want nil with an error", tc.source, got)
			}
			if !strings.HasPrefix(err.Error(), errPrefix) {
				t.Errorf("error = %q, want prefix %q", err, errPrefix)
			}
			var syntaxErr *json.SyntaxError
			var typeErr *json.UnmarshalTypeError
			switch tc.wantErr {
			case "syntax":
				if !errors.As(err, &syntaxErr) {
					t.Errorf("error %v does not wrap a JSON syntax error", err)
				}
			case "type":
				if !errors.As(err, &typeErr) {
					t.Errorf("error %v does not wrap a JSON type error", err)
				}
			}
		})
	}
}
