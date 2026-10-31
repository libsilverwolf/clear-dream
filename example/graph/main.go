// Command graph is a standalone demo of the clear-dream graph executor. It
// wires an LLM node and a ToolNode into a LangGraph-style agent loop:
//
//	START -> llm -+-> tools -> llm ...
//	              +-> END
//
// Without OPENAI_* configuration the demo uses a scripted offline client, so it
// runs without network access. Set OPENAI_API_KEY and/or OPENAI_BASE_URL to use
// a real OpenAI-compatible endpoint.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/libsilverwolf/clear-dream/agent"
	"github.com/libsilverwolf/clear-dream/model"
	"github.com/libsilverwolf/clear-dream/tools"
	"github.com/rotisserie/eris"
	"github.com/sirupsen/logrus"
)

const (
	defaultPrompt  = "What is 17 + 25? Use the add tool, then summarize the result."
	defaultBaseURL = "https://api.openai.com/v1"
	defaultModel   = "gpt-4o-mini"
	// maxSteps bounds the llm -> tools -> llm loop.
	maxSteps = 8
)

// agentState is the graph state: the running conversation plus bookkeeping.
type agentState struct {
	Messages []model.Message
	Answer   string
	Done     bool
	Trace    []string
}

func main() {
	// Keep stdout limited to the graph trace; warnings and errors still show.
	logrus.SetLevel(logrus.WarnLevel)

	prompt := strings.TrimSpace(strings.Join(os.Args[1:], " "))
	if prompt == "" {
		prompt = defaultPrompt
	}

	client, modelName, live := newLLMClient()

	registry := tools.NewToolRegistry()
	if err := registerTools(registry); err != nil {
		fail(err)
	}

	compiled, err := buildGraph(client, registry, modelName).Compile()
	if err != nil {
		fail(err)
	}

	result, err := compiled.Invoke(agentState{
		Messages: []model.Message{model.NewTextMessage(model.RoleUser, prompt)},
	})
	if err != nil {
		fail(err)
	}

	printResult(result, modelName, live)
}

// buildGraph wires the agent loop:
//
//	START -> llm -+-> tools -> llm ...
//	              +-> END
func buildGraph(client agent.OpenAiCompatableClient, registry *tools.ToolRegistry, modelName string) *agent.Graph[agentState] {
	g := agent.NewGraph[agentState]()

	// LLM node: sends the conversation and tool definitions, then records the
	// assistant message. It stops when the model answers without tool calls.
	g.AddNode("llm", func(state agentState) (agentState, error) {
		response, err := client.GetResponse(model.ChatCompletionRequest{
			Model:    modelName,
			Messages: state.Messages,
			Tools:    registry.GetToolDefinitions(),
		})
		if err != nil {
			return state, eris.Wrap(err, "completion request")
		}
		if len(response.Choices) == 0 {
			return state, eris.New("completion response contains no choices")
		}

		choice := response.Choices[0]
		assistant := choice.Message
		if assistant.Role == "" {
			assistant.Role = model.RoleAssistant
		}
		state.Messages = append(state.Messages, assistant)
		state.Trace = appendTrace(state.Trace, "llm")

		switch {
		case choice.FinishReason == "stop":
			state.Answer = assistant.Content
			state.Done = true
		case len(assistant.ToolCalls) == 0:
			return state, eris.Errorf("completion finished with reason %q and no tool calls", choice.FinishReason)
		default:
			state.Done = false
		}

		return state, nil
	})

	// ToolNode: executes every tool call requested by the last assistant
	// message and appends the results as tool messages.
	g.AddNode("tools", func(state agentState) (agentState, error) {
		if len(state.Messages) == 0 {
			return state, eris.New("no messages to resolve tool calls from")
		}
		last := state.Messages[len(state.Messages)-1]
		if len(last.ToolCalls) == 0 {
			return state, eris.New("last message contains no tool calls")
		}

		for _, call := range last.ToolCalls {
			// RunTool returns the ToolRunResult envelope as content. It only
			// returns no content for registry-level failures (for example an
			// unknown tool), which abort the run just like Model does.
			content, err := registry.RunTool(call, nil)
			if content == "" {
				if err == nil {
					return state, eris.Errorf("tool %q returned no content", call.Function.Name)
				}

				return state, eris.Wrapf(err, "run tool %q", call.Function.Name)
			}
			state.Messages = append(state.Messages, model.NewToolResponseMessage(call.ID, content))
			state.Trace = appendTrace(state.Trace, "tool:"+call.Function.Name)
		}

		return state, nil
	})

	g.AddEdge(agent.START, "llm")
	g.AddConditionalEdge("llm", func(state agentState) (string, error) {
		if state.Done {
			return agent.END, nil
		}

		return "tools", nil
	})
	g.AddEdge("tools", "llm")
	g.SetMaxSteps(maxSteps)

	return g
}

// newLLMClient returns a real client when OPENAI_API_KEY or OPENAI_BASE_URL is
// set, otherwise a scripted client that keeps the demo offline.
func newLLMClient() (agent.OpenAiCompatableClient, string, bool) {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")), "/")
	modelName := strings.TrimSpace(os.Getenv("OPENAI_MODEL"))

	if apiKey == "" && baseURL == "" {
		return &scriptedClient{}, "scripted", false
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if modelName == "" {
		modelName = defaultModel
	}

	return agent.NewDefaultOpenAiCompatableClient(agent.NewDefaultOpenAiCompatableClientParams{
		ApiKey: apiKey,
		ApiUrl: baseURL,
		Model:  modelName,
	}), modelName, true
}

// scriptedClient imitates an OpenAI-compatible model: the first turn asks for
// the add tool, the follow-up turn summarizes the tool result.
type scriptedClient struct{}

func (s *scriptedClient) GetResponse(request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	if len(request.Messages) == 0 {
		return model.ChatCompletionResponse{}, eris.New("scripted client: request contains no messages")
	}

	last := request.Messages[len(request.Messages)-1]
	if last.Role == model.RoleTool {
		var result tools.ToolRunResult
		if err := json.Unmarshal([]byte(last.Content), &result); err != nil {
			return model.ChatCompletionResponse{}, eris.Wrap(err, "scripted client: decode tool result")
		}
		if !result.Success {
			return scriptedResponse("stop", model.Message{
				Role:    model.RoleAssistant,
				Content: "The tool call failed: " + result.Error,
			}), nil
		}

		return scriptedResponse("stop", model.Message{
			Role:    model.RoleAssistant,
			Content: "The add tool returned " + result.Data + ".",
		}), nil
	}

	return scriptedResponse("tool_calls", model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{
			ID:   "call_scripted_1",
			Type: "function",
			Function: model.FunctionCallData{
				Name:      "add",
				Arguments: `{"A":17,"B":25}`,
			},
		}},
	}), nil
}

func scriptedResponse(finishReason string, message model.Message) model.ChatCompletionResponse {
	return model.ChatCompletionResponse{
		Choices: []model.Choice{{
			Index:        0,
			Message:      message,
			FinishReason: finishReason,
		}},
	}
}

// ==========================================================================
// Demo tools
// ==========================================================================

type addParams struct {
	A int `description:"first addend"`
	B int `description:"second addend"`
}

func registerTools(registry *tools.ToolRegistry) error {
	add, err := tools.CreateTool(tools.CreateToolParams{
		Name:        "add",
		Description: "Add two integers and return the sum.",
		ParamStruct: addParams{},
		Handler: func(input any, _ any) (any, error) {
			var params addParams
			if err := decodeArguments(input, &params); err != nil {
				return nil, err
			}

			return params.A + params.B, nil
		},
	})
	if err != nil {
		return eris.Wrap(err, "create add tool")
	}

	now, err := tools.CreateTool(tools.CreateToolParams{
		Name:        "current_time",
		Description: "Return the current local time in RFC3339 format.",
		Handler: func(_ any, _ any) (any, error) {
			return time.Now().Format(time.RFC3339), nil
		},
	})
	if err != nil {
		return eris.Wrap(err, "create current_time tool")
	}

	registry.RegisterTool("add", add)
	registry.RegisterTool("current_time", now)

	return nil
}

// decodeArguments parses the JSON argument string the model sent. Tools without
// parameters receive an empty string and leave target untouched.
func decodeArguments(input any, target any) error {
	raw, ok := input.(string)
	if !ok {
		return eris.Errorf("expected JSON argument string, got %T", input)
	}
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return eris.Wrap(err, "decode tool arguments")
	}

	return nil
}

func appendTrace(trace []string, node string) []string {
	next := make([]string, len(trace), len(trace)+1)
	copy(next, trace)

	return append(next, node)
}

func printResult(state agentState, modelName string, live bool) {
	backend := "scripted offline LLM"
	if live {
		backend = "live LLM (" + modelName + ")"
	}

	fmt.Println("=== graph agent loop ===")
	fmt.Printf("%-9s %s\n", "backend:", backend)
	fmt.Printf("%-9s %s\n", "trace:", strings.Join(state.Trace, " -> "))

	fmt.Println("\nmessages:")
	for i, message := range state.Messages {
		fmt.Printf("  %d. %-9s %s\n", i+1, message.Role+":", message.Content)
		for _, call := range message.ToolCalls {
			fmt.Printf("     tool_call: %s(%s)\n", call.Function.Name, call.Function.Arguments)
		}
	}

	fmt.Printf("\nanswer: %s\n", state.Answer)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "graph example: %v\n", err)
	os.Exit(1)
}
