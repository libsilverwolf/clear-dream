// Command graph is a standalone, offline demo of the clear-dream graph
// executor. It models a support-ticket triage pipeline as a DAG with a
// conditional edge and a fan-out/fan-in pair.
//
// Run it with:
//
//	go run ./example/graph
//	go run ./example/graph "Urgent: the dashboard is down after the last deploy"
//
// No API key or network access is required.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/libsilverwolf/clear-dream/agent"
	"github.com/rotisserie/eris"
)

const (
	defaultTicket = "Urgent: I was charged twice for the October invoice."
	// maxSteps bounds the number of supersteps; this pipeline needs five.
	maxSteps = 8
)

// ticketState is the graph's state. Nodes receive it by value, mutate their
// copy and return it for the next node.
type ticketState struct {
	Ticket   string
	Words    int
	Category string
	Priority string
	Owner    string
	Reply    string
	Audit    string
	Summary  string
	Trace    []string
}

// route maps a category to the node that handles it and the team that owns it.
// The slice order is the definition order, which also decides execution order
// for nodes activated in the same superstep.
type route struct {
	name  string
	owner string
}

var routes = []route{
	{name: "billing", owner: "finance team"},
	{name: "technical", owner: "platform team"},
	{name: "general", owner: "support team"},
}

func main() {
	ticket := strings.TrimSpace(strings.Join(os.Args[1:], " "))
	if ticket == "" {
		ticket = defaultTicket
	}

	compiled, err := buildGraph().Compile()
	if err != nil {
		fail(err)
	}

	result, err := compiled.Invoke(ticketState{Ticket: ticket})
	if err != nil {
		fail(err)
	}

	printResult(result)
}

// buildGraph wires the pipeline:
//
//	START -> parse -> classify -+-> billing   -+-> draft -+
//	                            +-> technical -+          +-> finish -> END
//	                            +-> general   -+-> audit -+
func buildGraph() *agent.Graph[ticketState] {
	g := agent.NewGraph[ticketState]()

	g.AddNode("parse", func(state ticketState) (ticketState, error) {
		state.Ticket = strings.TrimSpace(state.Ticket)
		if state.Ticket == "" {
			return state, eris.New("ticket is empty")
		}
		state.Words = len(strings.Fields(state.Ticket))
		state.Trace = appendTrace(state.Trace, "parse")

		return state, nil
	})

	g.AddNode("classify", func(state ticketState) (ticketState, error) {
		lower := strings.ToLower(state.Ticket)

		state.Category = "general"
		switch {
		case containsAny(lower, "invoice", "billing", "charge", "refund"):
			state.Category = "billing"
		case containsAny(lower, "down", "crash", "bug", "error", "broken"):
			state.Category = "technical"
		}

		state.Priority = "normal"
		if containsAny(lower, "urgent", "asap", "critical", "down") {
			state.Priority = "high"
		}
		state.Trace = appendTrace(state.Trace, "classify")

		return state, nil
	})

	for _, r := range routes {
		g.AddNode(r.name, func(state ticketState) (ticketState, error) {
			state.Owner = r.owner
			state.Trace = appendTrace(state.Trace, r.name)

			return state, nil
		})
		// Every handler fans out to the draft and audit nodes.
		g.AddEdge(r.name, "draft")
		g.AddEdge(r.name, "audit")
	}

	g.AddNode("draft", func(state ticketState) (ticketState, error) {
		state.Reply = fmt.Sprintf(
			"Thanks for reaching out. Your ticket has been assigned to the %s with %s priority.",
			state.Owner, state.Priority,
		)
		state.Trace = appendTrace(state.Trace, "draft")

		return state, nil
	})

	g.AddNode("audit", func(state ticketState) (ticketState, error) {
		if state.Owner == "" || state.Category == "" {
			return state, eris.New("ticket was not classified correctly")
		}
		state.Audit = "passed"
		state.Trace = appendTrace(state.Trace, "audit")

		return state, nil
	})

	g.AddNode("finish", func(state ticketState) (ticketState, error) {
		if state.Audit != "passed" {
			return state, eris.New("audit did not pass")
		}
		state.Summary = fmt.Sprintf("assigned to %s: %s", state.Owner, state.Reply)
		state.Trace = appendTrace(state.Trace, "finish")

		return state, nil
	})

	g.AddEdge(agent.START, "parse")
	g.AddEdge("parse", "classify")
	g.AddConditionalEdge("classify", routeByCategory)
	g.AddEdge("draft", "finish")
	g.AddEdge("audit", "finish")
	g.AddEdge("finish", agent.END)
	g.SetMaxSteps(maxSteps)

	return g
}

// routeByCategory picks the handler node from the category written by classify.
func routeByCategory(state ticketState) (string, error) {
	if state.Category == "" {
		return "", eris.New("category is missing")
	}

	return state.Category, nil
}

// appendTrace returns a copy of the trace so each node publishes a new slice
// instead of mutating the slice header shared with the previous state.
func appendTrace(trace []string, node string) []string {
	next := make([]string, len(trace), len(trace)+1)
	copy(next, trace)

	return append(next, node)
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}

	return false
}

func printResult(state ticketState) {
	fmt.Println("=== support ticket triage ===")
	fmt.Printf("%-9s %s\n", "ticket:", state.Ticket)
	fmt.Printf("%-9s %d\n", "words:", state.Words)
	fmt.Printf("%-9s %s\n", "category:", state.Category)
	fmt.Printf("%-9s %s\n", "priority:", state.Priority)
	fmt.Printf("%-9s %s\n", "owner:", state.Owner)
	fmt.Printf("%-9s %s\n", "audit:", state.Audit)
	fmt.Printf("%-9s %s\n", "reply:", state.Reply)
	fmt.Printf("%-9s %s\n", "summary:", state.Summary)
	fmt.Printf("%-9s %s\n", "trace:", strings.Join(state.Trace, " -> "))

	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		fail(eris.Wrap(err, "encode final state"))
	}
	fmt.Printf("\nfinal state:\n%s\n", encoded)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "graph example: %v\n", err)
	os.Exit(1)
}
