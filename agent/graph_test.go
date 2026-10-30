package agent

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/rotisserie/eris"
)

// graphState is the shared state type used by most graph tests.
type graphState struct {
	Input   string
	Raw     string
	Result  string
	N       int
	Kind    string
	Answer  string
	Left    bool
	Right   bool
	Always  bool
	Maybe   bool
	Checked bool
}

// ==========================================================================
// Happy paths
// ==========================================================================

func TestGraphLinearRun(t *testing.T) {
	var ran []string

	g := NewGraph[graphState]()
	g.AddNode("fetch", func(state graphState) (graphState, error) {
		ran = append(ran, "fetch")
		state.Raw = "data"
		return state, nil
	})
	g.AddNode("transform", func(state graphState) (graphState, error) {
		ran = append(ran, "transform")
		state.Result = strings.ToUpper(state.Raw)
		return state, nil
	})
	g.AddEdge(START, "fetch")
	g.AddEdge("fetch", "transform")
	g.AddEdge("transform", END)

	compiled, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}

	result, err := compiled.Invoke(graphState{Input: "seed"})
	if err != nil {
		t.Fatalf("Invoke() error = %v, want nil", err)
	}

	if result.Input != "seed" || result.Raw != "data" || result.Result != "DATA" {
		t.Fatalf("Invoke() = %#v, want the state threaded through both nodes", result)
	}
	if wantOrder := []string{"fetch", "transform"}; !reflect.DeepEqual(ran, wantOrder) {
		t.Fatalf("node execution order = %v, want %v", ran, wantOrder)
	}
}

func TestGraphNodeReceivesCopy(t *testing.T) {
	g := NewGraph[graphState]()
	g.AddNode("mutate", func(state graphState) (graphState, error) {
		state.Input = "mutated"
		return graphState{Result: "kept"}, nil
	})
	g.AddEdge(START, "mutate")
	g.AddEdge("mutate", END)

	compiled, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}

	result, err := compiled.Invoke(graphState{Input: "original"})
	if err != nil {
		t.Fatalf("Invoke() error = %v, want nil", err)
	}

	// The node received a copy; only the state it returned is kept.
	if result.Input != "" {
		t.Fatalf("Invoke().Input = %q, want it to be dropped with the returned state", result.Input)
	}
	if result.Result != "kept" {
		t.Fatalf("Invoke().Result = %q, want %q", result.Result, "kept")
	}
}

func TestGraphNodeWithoutUpdates(t *testing.T) {
	g := NewGraph[graphState]()
	g.AddNode("noop", func(state graphState) (graphState, error) { return state, nil })
	g.AddEdge(START, "noop")

	compiled, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}

	result, err := compiled.Invoke(graphState{Input: "keep me"})
	if err != nil {
		t.Fatalf("Invoke() error = %v, want nil", err)
	}
	if result.Input != "keep me" {
		t.Fatalf("Invoke().Input = %q, want the state to pass through", result.Input)
	}
}

func TestGraphFanOutAndJoin(t *testing.T) {
	executed := make(map[string]int)

	g := NewGraph[graphState]()
	g.AddNode("root", func(state graphState) (graphState, error) {
		executed["root"]++
		state.N = 0
		return state, nil
	})
	g.AddNode("left", func(state graphState) (graphState, error) {
		executed["left"]++
		state.Left = true
		return state, nil
	})
	g.AddNode("right", func(state graphState) (graphState, error) {
		executed["right"]++
		state.Right = true
		return state, nil
	})
	g.AddNode("join", func(state graphState) (graphState, error) {
		executed["join"]++
		state.N++
		return state, nil
	})
	g.AddEdge(START, "root")
	g.AddEdge("root", "left")
	g.AddEdge("root", "right")
	g.AddEdge("left", "join")
	g.AddEdge("right", "join")
	g.AddEdge("join", END)

	compiled, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}

	result, err := compiled.Invoke(graphState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v, want nil", err)
	}

	if !result.Left || !result.Right {
		t.Fatalf("Invoke() = %#v, want both branches to have run", result)
	}
	if result.N != 1 {
		t.Fatalf("Invoke().N = %d, want 1 (join must run once)", result.N)
	}
	for _, name := range []string{"root", "left", "right", "join"} {
		if executed[name] != 1 {
			t.Fatalf("node %q executed %d times, want 1", name, executed[name])
		}
	}
}

func TestGraphConditionalRouting(t *testing.T) {
	g := NewGraph[graphState]()
	g.AddNode("classify", func(state graphState) (graphState, error) {
		if state.N%2 == 0 {
			state.Kind = "even"
		} else {
			state.Kind = "odd"
		}
		return state, nil
	})
	g.AddNode("even", func(state graphState) (graphState, error) {
		state.Answer = "even branch"
		return state, nil
	})
	g.AddNode("odd", func(state graphState) (graphState, error) {
		state.Answer = "odd branch"
		return state, nil
	})
	// The router must see the update written by classify.
	g.AddConditionalEdge("classify", func(state graphState) (string, error) {
		return state.Kind, nil
	})
	g.AddEdge(START, "classify")
	g.AddEdge("even", END)
	g.AddEdge("odd", END)

	compiled, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}

	tests := []struct {
		name string
		n    int
		want string
	}{
		{name: "even", n: 2, want: "even branch"},
		{name: "odd", n: 3, want: "odd branch"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := compiled.Invoke(graphState{N: tc.n})
			if err != nil {
				t.Fatalf("Invoke() error = %v, want nil", err)
			}
			if result.Answer != tc.want {
				t.Fatalf("Invoke().Answer = %q, want %q", result.Answer, tc.want)
			}
		})
	}
}

func TestGraphConditionalEdgeToEnd(t *testing.T) {
	g := NewGraph[graphState]()
	g.AddNode("guard", func(state graphState) (graphState, error) {
		state.Checked = true
		return state, nil
	})
	g.AddConditionalEdge("guard", func(state graphState) (string, error) {
		return END, nil
	})
	g.AddEdge(START, "guard")

	compiled, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}

	result, err := compiled.Invoke(graphState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v, want nil", err)
	}
	if !result.Checked {
		t.Fatalf("Invoke() = %#v, want the node update to survive", result)
	}
}

func TestGraphStaticAndConditionalEdgesCombine(t *testing.T) {
	var ran []string

	g := NewGraph[graphState]()
	g.AddNode("source", func(state graphState) (graphState, error) {
		ran = append(ran, "source")
		return state, nil
	})
	g.AddNode("always", func(state graphState) (graphState, error) {
		ran = append(ran, "always")
		state.Always = true
		return state, nil
	})
	g.AddNode("maybe", func(state graphState) (graphState, error) {
		ran = append(ran, "maybe")
		state.Maybe = true
		return state, nil
	})
	g.AddEdge(START, "source")
	g.AddEdge("source", "always")
	g.AddConditionalEdge("source", func(state graphState) (string, error) {
		return "maybe", nil
	})

	compiled, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}
	result, err := compiled.Invoke(graphState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v, want nil", err)
	}

	if !result.Always || !result.Maybe {
		t.Fatalf("Invoke() = %#v, want both static and conditional targets to run", result)
	}
	if want := []string{"source", "always", "maybe"}; !reflect.DeepEqual(ran, want) {
		t.Fatalf("node execution order = %v, want %v", ran, want)
	}
}

func TestGraphMultipleEntryPoints(t *testing.T) {
	executed := make(map[string]int)

	g := NewGraph[graphState]()
	g.AddNode("a", func(state graphState) (graphState, error) {
		executed["a"]++
		state.Left = true
		return state, nil
	})
	g.AddNode("b", func(state graphState) (graphState, error) {
		executed["b"]++
		state.Right = true
		return state, nil
	})
	g.AddEdge(START, "a")
	g.AddEdge(START, "b")
	// Duplicate START edges must not run the node twice per step.
	g.AddEdge(START, "a")

	compiled, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}

	result, err := compiled.Invoke(graphState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v, want nil", err)
	}

	if !result.Left || !result.Right {
		t.Fatalf("Invoke() = %#v, want both entry nodes to run", result)
	}
	if executed["a"] != 1 || executed["b"] != 1 {
		t.Fatalf("executions = %v, want each entry to run exactly once", executed)
	}
}

func TestGraphZeroInitialState(t *testing.T) {
	g := NewGraph[graphState]()
	g.AddNode("init", func(state graphState) (graphState, error) {
		state.Checked = true
		return state, nil
	})
	g.AddEdge(START, "init")

	compiled, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}

	result, err := compiled.Invoke(graphState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v, want nil", err)
	}
	if !result.Checked {
		t.Fatalf("Invoke() = %#v, want checked=true", result)
	}
}

func TestGraphZeroValueBuilder(t *testing.T) {
	var g Graph[graphState]
	g.AddNode("only", func(state graphState) (graphState, error) {
		state.Checked = true
		return state, nil
	})
	g.AddEdge(START, "only")

	compiled, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}
	result, err := compiled.Invoke(graphState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v, want nil", err)
	}
	if !result.Checked {
		t.Fatalf("Invoke() = %#v, want checked=true", result)
	}
}

func TestGraphSupportsMapState(t *testing.T) {
	g := NewGraph[map[string]any]()
	g.AddNode("mark", func(state map[string]any) (map[string]any, error) {
		state["done"] = true
		return state, nil
	})
	g.AddEdge(START, "mark")

	compiled, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}

	result, err := compiled.Invoke(map[string]any{"input": "x"})
	if err != nil {
		t.Fatalf("Invoke() error = %v, want nil", err)
	}
	if result["done"] != true || result["input"] != "x" {
		t.Fatalf("Invoke() = %#v, want both keys", result)
	}
}

func TestGraphCompiledSnapshot(t *testing.T) {
	executed := false

	g := NewGraph[graphState]()
	g.AddNode("first", func(state graphState) (graphState, error) { return state, nil })
	g.AddEdge(START, "first")
	g.SetMaxSteps(1)

	compiled, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}

	// Changes after Compile must not leak into the compiled graph.
	g.AddNode("late", func(state graphState) (graphState, error) {
		executed = true
		return state, nil
	})
	g.AddEdge(START, "late")
	g.SetMaxSteps(99)

	if compiled.maxSteps != 1 {
		t.Fatalf("compiled.maxSteps = %d, want the value from Compile (1)", compiled.maxSteps)
	}
	if _, err := compiled.Invoke(graphState{}); err != nil {
		t.Fatalf("Invoke() error = %v, want nil", err)
	}
	if executed {
		t.Fatal("late node ran, want the compiled graph to be an immutable snapshot")
	}
}

// ==========================================================================
// Step limits
// ==========================================================================

func TestGraphMaxSteps(t *testing.T) {
	newCycle := func() *Graph[graphState] {
		g := NewGraph[graphState]()
		g.AddNode("a", func(state graphState) (graphState, error) { return state, nil })
		g.AddNode("b", func(state graphState) (graphState, error) { return state, nil })
		g.AddEdge(START, "a")
		g.AddEdge("a", "b")
		g.AddEdge("b", "a")

		return g
	}

	t.Run("explicit limit", func(t *testing.T) {
		g := newCycle()
		g.SetMaxSteps(3)

		compiled, err := g.Compile()
		if err != nil {
			t.Fatalf("Compile() error = %v, want nil", err)
		}
		_, err = compiled.Invoke(graphState{})
		if err == nil {
			t.Fatal("Invoke() error = nil, want a step limit error")
		}
		if !strings.Contains(err.Error(), "exceeded max steps (3)") {
			t.Fatalf("Invoke() error = %v, want it to mention the limit", err)
		}
	})

	t.Run("default limit", func(t *testing.T) {
		compiled, err := newCycle().Compile()
		if err != nil {
			t.Fatalf("Compile() error = %v, want nil", err)
		}
		_, err = compiled.Invoke(graphState{})
		if err == nil {
			t.Fatal("Invoke() error = nil, want a step limit error")
		}
		if !strings.Contains(err.Error(), "exceeded max steps (25)") {
			t.Fatalf("Invoke() error = %v, want the default limit", err)
		}
	})

	t.Run("single step is enough for a leaf", func(t *testing.T) {
		g := NewGraph[graphState]()
		g.AddNode("leaf", func(state graphState) (graphState, error) {
			state.Checked = true
			return state, nil
		})
		g.AddEdge(START, "leaf")
		g.SetMaxSteps(1)

		compiled, err := g.Compile()
		if err != nil {
			t.Fatalf("Compile() error = %v, want nil", err)
		}
		if _, err := compiled.Invoke(graphState{}); err != nil {
			t.Fatalf("Invoke() error = %v, want nil", err)
		}
	})
}

// ==========================================================================
// Definition errors
// ==========================================================================

func TestGraphDefinitionErrors(t *testing.T) {
	node := func(state graphState) (graphState, error) { return state, nil }
	router := func(state graphState) (string, error) { return END, nil }

	tests := []struct {
		name  string
		build func() *Graph[graphState]
		want  string
	}{
		{
			name:  "no nodes",
			build: func() *Graph[graphState] { return NewGraph[graphState]() },
			want:  "without nodes",
		},
		{
			name: "no entry point",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("a", node)
				g.AddEdge("a", END)
				return g
			},
			want: "no entry point",
		},
		{
			name: "entry is END",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("a", node)
				g.AddEdge(START, END)
				return g
			},
			want: "entry point cannot be END",
		},
		{
			name: "entry edge to unknown node",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("a", node)
				g.AddEdge(START, "ghost")
				return g
			},
			want: "entry edge references unknown node",
		},
		{
			name: "edge from unknown node",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("a", node)
				g.AddEdge(START, "a")
				g.AddEdge("ghost", "a")
				return g
			},
			want: "edge from unknown node",
		},
		{
			name: "edge to unknown node",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("a", node)
				g.AddEdge(START, "a")
				g.AddEdge("a", "ghost")
				return g
			},
			want: "references an unknown node",
		},
		{
			name: "edge from END",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("a", node)
				g.AddEdge(START, "a")
				g.AddEdge(END, "a")
				return g
			},
			want: "cannot add edges from",
		},
		{
			name: "edge to START",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("a", node)
				g.AddEdge(START, "a")
				g.AddEdge("a", START)
				return g
			},
			want: "is not allowed",
		},
		{
			name: "conditional edge from unknown node",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("a", node)
				g.AddEdge(START, "a")
				g.AddConditionalEdge("ghost", router)
				return g
			},
			want: "conditional edge from unknown node",
		},
		{
			name: "conditional edge from START",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("a", node)
				g.AddEdge(START, "a")
				g.AddConditionalEdge(START, router)
				return g
			},
			want: "conditional edges from START are not supported",
		},
		{
			name: "conditional edge from END",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("a", node)
				g.AddEdge(START, "a")
				g.AddConditionalEdge(END, router)
				return g
			},
			want: "cannot add conditional edges from",
		},
		{
			name: "duplicate conditional edge",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("a", node)
				g.AddEdge(START, "a")
				g.AddConditionalEdge("a", router)
				g.AddConditionalEdge("a", router)
				return g
			},
			want: "already defined",
		},
		{
			name: "duplicate node",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("a", node)
				g.AddNode("a", node)
				return g
			},
			want: `node "a" is already defined`,
		},
		{
			name: "empty node name",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("", node)
				return g
			},
			want: "node name cannot be empty",
		},
		{
			name: "reserved node name",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode(START, node)
				return g
			},
			want: "is reserved",
		},
		{
			name: "nil node function",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("a", nil)
				return g
			},
			want: "nil function",
		},
		{
			name: "empty edge endpoint",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddEdge("", "a")
				return g
			},
			want: "edge endpoints cannot be empty",
		},
		{
			name: "empty conditional edge source",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddConditionalEdge("", router)
				return g
			},
			want: "conditional edge source cannot be empty",
		},
		{
			name: "nil router",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("a", node)
				g.AddEdge(START, "a")
				g.AddConditionalEdge("a", nil)
				return g
			},
			want: "nil router",
		},
		{
			name: "zero max steps",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("a", node)
				g.AddEdge(START, "a")
				g.SetMaxSteps(0)
				return g
			},
			want: "max steps must be positive",
		},
		{
			name: "negative max steps",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("a", node)
				g.AddEdge(START, "a")
				g.SetMaxSteps(-3)
				return g
			},
			want: "got -3",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			compiled, err := tc.build().Compile()
			if err == nil {
				t.Fatalf("Compile() error = nil, want it to contain %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Compile() error = %v, want it to contain %q", err, tc.want)
			}
			if compiled != nil {
				t.Fatalf("Compile() = %#v, want nil on error", compiled)
			}
		})
	}
}

// ==========================================================================
// Runtime errors
// ==========================================================================

func TestGraphRuntimeErrors(t *testing.T) {
	node := func(state graphState) (graphState, error) { return state, nil }

	tests := []struct {
		name  string
		build func() *Graph[graphState]
		want  []string
	}{
		{
			name: "node failure",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("boom", func(state graphState) (graphState, error) {
					return state, eris.New("kaboom")
				})
				g.AddEdge(START, "boom")
				return g
			},
			want: []string{`node "boom" failed`, "kaboom"},
		},
		{
			name: "router failure",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("route", node)
				g.AddConditionalEdge("route", func(state graphState) (string, error) {
					return "", eris.New("router exploded")
				})
				g.AddEdge(START, "route")
				return g
			},
			want: []string{`conditional edge from "route" failed`, "router exploded"},
		},
		{
			name: "router returns empty name",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("route", node)
				g.AddConditionalEdge("route", func(state graphState) (string, error) {
					return "", nil
				})
				g.AddEdge(START, "route")
				return g
			},
			want: []string{"empty node name"},
		},
		{
			name: "router returns unknown node",
			build: func() *Graph[graphState] {
				g := NewGraph[graphState]()
				g.AddNode("route", node)
				g.AddConditionalEdge("route", func(state graphState) (string, error) {
					return "ghost", nil
				})
				g.AddEdge(START, "route")
				return g
			},
			want: []string{`routed to unknown node "ghost"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			compiled, err := tc.build().Compile()
			if err != nil {
				t.Fatalf("Compile() error = %v, want nil", err)
			}

			result, err := compiled.Invoke(graphState{})
			if err == nil {
				t.Fatal("Invoke() error = nil, want an error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("Invoke() error = %v, want it to contain %q", err, want)
				}
			}
			if result != (graphState{}) {
				t.Fatalf("Invoke() = %#v, want the zero state on error", result)
			}
		})
	}
}

// ==========================================================================
// Concurrency
// ==========================================================================

func TestCompiledGraphConcurrentInvoke(t *testing.T) {
	g := NewGraph[graphState]()
	g.AddNode("inc", func(state graphState) (graphState, error) {
		state.N++
		return state, nil
	})
	g.AddEdge(START, "inc")
	g.AddEdge("inc", END)

	compiled, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}

	const workers = 16
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()

			result, err := compiled.Invoke(graphState{N: i})
			if err != nil {
				t.Errorf("Invoke(%d) error = %v, want nil", i, err)
				return
			}
			if result.N != i+1 {
				t.Errorf("Invoke(%d).N = %d, want %d", i, result.N, i+1)
			}
		}(i)
	}
	wg.Wait()
}
