package agent

import (
	"errors"
	"strings"

	"github.com/rotisserie/eris"
	"github.com/sirupsen/logrus"
)

const (
	// START is the virtual source node. Add an edge from START to declare an
	// entry point; multiple START edges start multiple branches.
	START = "__start__"
	// END is the virtual sink node. Returning it from a router or pointing a
	// static edge at it terminates that branch.
	END = "__end__"

	// defaultMaxSteps bounds the number of supersteps when SetMaxSteps is not
	// used. It protects against cycles that never reach END.
	defaultMaxSteps = 25
)

// NodeFunc executes one graph node. It receives the current state by value and
// returns the state for the next node; returning the receiver unchanged means
// "no changes".
type NodeFunc[S any] func(state S) (S, error)

// RouterFunc chooses the next node after the current one. It receives the state
// after the previous node completed and returns a node name or END to stop that
// branch.
type RouterFunc[S any] func(state S) (string, error)

// Graph builds a directed graph over a caller-defined state type S. Builder
// methods are chainable and record definition errors; Compile reports them. The
// zero value is ready to use. A Graph is not safe for concurrent mutation.
type Graph[S any] struct {
	logger      *logrus.Entry
	nodes       map[string]NodeFunc[S]
	order       []string
	edges       map[string][]string
	conditional map[string]RouterFunc[S]
	maxSteps    int
	errs        []error
}

// NewGraph returns an empty graph with the default step limit.
func NewGraph[S any]() *Graph[S] {
	g := &Graph[S]{}
	g.init()

	return g
}

func (g *Graph[S]) init() {
	if g.logger == nil {
		g.logger = logrus.WithField("module", "graph")
	}
	if g.nodes == nil {
		g.nodes = make(map[string]NodeFunc[S])
	}
	if g.edges == nil {
		g.edges = make(map[string][]string)
	}
	if g.conditional == nil {
		g.conditional = make(map[string]RouterFunc[S])
	}
	if g.maxSteps <= 0 {
		g.maxSteps = defaultMaxSteps
	}
}

func (g *Graph[S]) fail(err error) {
	g.errs = append(g.errs, err)
}

// AddNode registers a node under name. Names must be unique, non-empty and must
// not collide with START or END.
func (g *Graph[S]) AddNode(name string, fn NodeFunc[S]) *Graph[S] {
	g.init()

	switch {
	case name == "":
		g.fail(eris.New("graph: node name cannot be empty"))
	case name == START || name == END:
		g.fail(eris.Errorf("graph: %q is reserved and cannot be used as a node name", name))
	case fn == nil:
		g.fail(eris.Errorf("graph: node %q has a nil function", name))
	default:
		if _, exists := g.nodes[name]; exists {
			g.fail(eris.Errorf("graph: node %q is already defined", name))
			return g
		}
		g.nodes[name] = fn
		g.order = append(g.order, name)
	}

	return g
}

// AddEdge declares that every completion of from activates to. Adding several
// outgoing edges from the same node fans out; END terminates the branch.
func (g *Graph[S]) AddEdge(from, to string) *Graph[S] {
	g.init()

	if from == "" || to == "" {
		g.fail(eris.New("graph: edge endpoints cannot be empty"))
		return g
	}
	g.edges[from] = append(g.edges[from], to)

	return g
}

// AddConditionalEdge installs a router that runs after from and selects the next
// node at runtime. Only one conditional edge per source is allowed.
func (g *Graph[S]) AddConditionalEdge(from string, router RouterFunc[S]) *Graph[S] {
	g.init()

	switch {
	case from == "":
		g.fail(eris.New("graph: conditional edge source cannot be empty"))
	case router == nil:
		g.fail(eris.Errorf("graph: conditional edge from %q has a nil router", from))
	default:
		if _, exists := g.conditional[from]; exists {
			g.fail(eris.Errorf("graph: conditional edge from %q is already defined", from))
			return g
		}
		g.conditional[from] = router
	}

	return g
}

// SetMaxSteps limits how many supersteps Invoke may run. Values below one are
// rejected during Compile.
func (g *Graph[S]) SetMaxSteps(steps int) *Graph[S] {
	g.init()

	if steps <= 0 {
		g.fail(eris.Errorf("graph: max steps must be positive, got %d", steps))
		return g
	}
	g.maxSteps = steps

	return g
}

// Compile validates the graph and returns an immutable, runnable copy of it.
// Later changes to the builder do not affect the compiled graph.
func (g *Graph[S]) Compile() (*CompiledGraph[S], error) {
	g.init()

	if err := errors.Join(g.errs...); err != nil {
		return nil, eris.Wrap(err, "graph: invalid definition")
	}
	if len(g.nodes) == 0 {
		return nil, eris.New("graph: cannot compile a graph without nodes")
	}

	entries, err := g.resolveEntries()
	if err != nil {
		return nil, err
	}
	if err := g.validateEdges(); err != nil {
		return nil, err
	}
	if err := g.validateConditionalEdges(); err != nil {
		return nil, err
	}

	compiled := &CompiledGraph[S]{
		logger:      g.logger,
		nodes:       make(map[string]NodeFunc[S], len(g.nodes)),
		order:       append([]string(nil), g.order...),
		edges:       make(map[string][]string, len(g.edges)),
		conditional: make(map[string]RouterFunc[S], len(g.conditional)),
		entries:     entries,
		maxSteps:    g.maxSteps,
	}
	for name, fn := range g.nodes {
		compiled.nodes[name] = fn
	}
	for from, targets := range g.edges {
		compiled.edges[from] = append([]string(nil), targets...)
	}
	for from, router := range g.conditional {
		compiled.conditional[from] = router
	}

	return compiled, nil
}

func (g *Graph[S]) resolveEntries() ([]string, error) {
	seen := make(map[string]bool, len(g.edges[START]))
	entries := make([]string, 0, len(g.edges[START]))

	for _, target := range g.edges[START] {
		if target == END {
			return nil, eris.New("graph: entry point cannot be END")
		}
		if _, ok := g.nodes[target]; !ok {
			return nil, eris.Errorf("graph: entry edge references unknown node %q", target)
		}
		if seen[target] {
			continue
		}
		seen[target] = true
		entries = append(entries, target)
	}

	if len(entries) == 0 {
		return nil, eris.Errorf("graph: no entry point; add an edge from %s", START)
	}

	return entries, nil
}

func (g *Graph[S]) validateEdges() error {
	for from, targets := range g.edges {
		switch from {
		case START:
			continue
		case END:
			return eris.Errorf("graph: cannot add edges from %s", END)
		}

		if _, ok := g.nodes[from]; !ok {
			return eris.Errorf("graph: edge from unknown node %q", from)
		}

		for _, to := range targets {
			switch to {
			case START:
				return eris.Errorf("graph: edge %q -> %s is not allowed", from, START)
			case END:
				continue
			default:
				if _, ok := g.nodes[to]; !ok {
					return eris.Errorf("graph: edge %q -> %q references an unknown node", from, to)
				}
			}
		}
	}

	return nil
}

func (g *Graph[S]) validateConditionalEdges() error {
	for from := range g.conditional {
		switch from {
		case START:
			return eris.New("graph: conditional edges from START are not supported; use AddEdge")
		case END:
			return eris.Errorf("graph: cannot add conditional edges from %s", END)
		}

		if _, ok := g.nodes[from]; !ok {
			return eris.Errorf("graph: conditional edge from unknown node %q", from)
		}
	}

	return nil
}

// CompiledGraph is a validated graph ready to run. It is immutable and safe for
// concurrent Invoke calls as long as the node and router functions are.
type CompiledGraph[S any] struct {
	logger      *logrus.Entry
	nodes       map[string]NodeFunc[S]
	order       []string
	edges       map[string][]string
	conditional map[string]RouterFunc[S]
	entries     []string
	maxSteps    int
}

// Invoke runs the graph until no branch is active and returns the final state.
// Nodes activated in the same superstep run in definition order; each node
// receives the state returned by the previous one. On error the zero value of S
// is returned.
func (c *CompiledGraph[S]) Invoke(initial S) (S, error) {
	state := initial
	active := make(map[string]bool, len(c.entries))
	for _, name := range c.entries {
		active[name] = true
	}

	for step := 0; len(active) > 0; step++ {
		if step >= c.maxSteps {
			return zero[S](), eris.Errorf("graph: exceeded max steps (%d) with nodes %s still active",
				c.maxSteps, strings.Join(nodesInOrder(c.order, active), ", "))
		}

		next := make(map[string]bool)
		for _, name := range c.order {
			if !active[name] {
				continue
			}

			c.logger.WithFields(logrus.Fields{"node": name, "step": step}).Debug("running graph node")

			updated, err := c.nodes[name](state)
			if err != nil {
				return zero[S](), eris.Wrapf(err, "graph: node %q failed", name)
			}
			state = updated

			for _, target := range c.edges[name] {
				if target != END {
					next[target] = true
				}
			}

			router, ok := c.conditional[name]
			if !ok {
				continue
			}

			target, err := router(state)
			if err != nil {
				return zero[S](), eris.Wrapf(err, "graph: conditional edge from %q failed", name)
			}
			switch target {
			case "":
				return zero[S](), eris.Errorf("graph: conditional edge from %q returned an empty node name", name)
			case END:
				continue
			}
			if _, ok := c.nodes[target]; !ok {
				return zero[S](), eris.Errorf("graph: conditional edge from %q routed to unknown node %q", name, target)
			}
			next[target] = true
		}

		active = next
	}

	return state, nil
}

func zero[S any]() S {
	var zero S
	return zero
}

func nodesInOrder(order []string, active map[string]bool) []string {
	names := make([]string, 0, len(active))
	for _, name := range order {
		if active[name] {
			names = append(names, name)
		}
	}

	return names
}
