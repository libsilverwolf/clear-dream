package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/libsilverwolf/clear-dream/model"
	"github.com/libsilverwolf/clear-dream/tools"
	"github.com/rotisserie/eris"
)

// ==========================================================================
// Fakes
// ==========================================================================

type scriptedResponse struct {
	response model.ChatCompletionResponse
	err      error
}

// fakeApiClient replays a scripted list of responses and records every request
// it receives. Requests beyond the script fail loudly instead of hanging.
type fakeApiClient struct {
	mu       sync.Mutex
	script   []scriptedResponse
	requests []model.ChatCompletionRequest
	calls    int
}

func (f *fakeApiClient) GetResponse(request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.requests = append(f.requests, request)
	if f.calls >= len(f.script) {
		return model.ChatCompletionResponse{}, eris.Errorf("fake api client: unexpected request #%d", len(f.requests))
	}

	item := f.script[f.calls]
	f.calls++

	return item.response, item.err
}

func (f *fakeApiClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeApiClient) requestAt(t *testing.T, index int) model.ChatCompletionRequest {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()
	if index >= len(f.requests) {
		t.Fatalf("request #%d was not recorded, only %d request(s) made", index, len(f.requests))
	}

	return f.requests[index]
}

func stopResponse(content string) scriptedResponse {
	return scriptedResponse{response: model.ChatCompletionResponse{
		Choices: []model.Choice{{
			Index:        0,
			Message:      model.NewTextMessage(model.RoleAssistant, content),
			FinishReason: "stop",
		}},
	}}
}

func toolCallResponse(toolCalls ...model.ToolCall) scriptedResponse {
	return scriptedResponse{response: model.ChatCompletionResponse{
		Choices: []model.Choice{{
			Index:        0,
			Message:      model.Message{Role: model.RoleAssistant, ToolCalls: toolCalls},
			FinishReason: "tool_calls",
		}},
	}}
}

func newToolCall(id, name, arguments string) model.ToolCall {
	return model.ToolCall{
		ID:   id,
		Type: "function",
		Function: model.FunctionCallData{
			Name:      name,
			Arguments: arguments,
		},
	}
}

// echoParams gives the helper tools one argument so their definitions are
// non-trivial.
type echoParams struct {
	Text string `description:"text to pass through"`
}

func registerTool(t *testing.T, registry *tools.ToolRegistry, name string, handler func(input any, ctx any) (any, error)) {
	t.Helper()

	tool, err := tools.CreateTool(tools.CreateToolParams{
		Name:        name,
		Description: "test tool " + name,
		Handler:     handler,
		ParamStruct: echoParams{},
	})
	if err != nil {
		t.Fatalf("CreateTool(%q) error = %v, want nil", name, err)
	}

	registry.RegisterTool(name, tool)
}

func newTestModel(registry *tools.ToolRegistry, client OpenAiCompatableClient) *Model {
	if registry == nil {
		registry = tools.NewToolRegistry()
	}

	return NewModel(&NewModelParams{
		ToolsRegistry: registry,
		ApiClient:     client,
		DefaultModel:  "test-model",
	})
}

type toolResultPayload struct {
	Success bool
	Error   string
	Content string
}

func decodeToolResult(t *testing.T, message model.Message) toolResultPayload {
	t.Helper()

	var payload toolResultPayload
	if err := json.Unmarshal([]byte(message.Content), &payload); err != nil {
		t.Fatalf("decode tool message content %q: %v", message.Content, err)
	}

	return payload
}

// ==========================================================================
// NewModel
// ==========================================================================

func TestNewModelConfiguresFields(t *testing.T) {
	registry := tools.NewToolRegistry()
	client := &fakeApiClient{}

	a := NewModel(&NewModelParams{
		ToolsRegistry: registry,
		ApiClient:     client,
		DefaultModel:  "custom-model",
	})

	if a.logger == nil {
		t.Fatal("Model.logger = nil, want a logger")
	}
	if a.toolsRegistry != registry {
		t.Fatalf("Model.toolsRegistry = %p, want %p", a.toolsRegistry, registry)
	}
	if a.apiClient != OpenAiCompatableClient(client) {
		t.Fatalf("Model.apiClient = %#v, want the provided client", a.apiClient)
	}
	if a.defaultModel != "custom-model" {
		t.Fatalf("Model.defaultModel = %q, want %q", a.defaultModel, "custom-model")
	}
}

func TestNewModelFallsBackToDefaultModel(t *testing.T) {
	a := NewModel(&NewModelParams{
		ToolsRegistry: tools.NewToolRegistry(),
		ApiClient:     &fakeApiClient{},
	})

	if a.defaultModel != defaultModel {
		t.Fatalf("Model.defaultModel = %q, want the package default %q", a.defaultModel, defaultModel)
	}
}

// ==========================================================================
// Invoke
// ==========================================================================

func TestInvokeReturnsContentWhenFinishReasonIsStop(t *testing.T) {
	client := &fakeApiClient{script: []scriptedResponse{stopResponse("hello there")}}
	a := newTestModel(nil, client)

	output, err := a.Invoke("say hi")
	if err != nil {
		t.Fatalf("Invoke() error = %v, want nil", err)
	}
	if output != "hello there" {
		t.Fatalf("Invoke() = %q, want %q", output, "hello there")
	}
	if client.callCount() != 1 {
		t.Fatalf("api client calls = %d, want 1", client.callCount())
	}

	request := client.requestAt(t, 0)
	if request.Model != "test-model" {
		t.Fatalf("request.Model = %q, want %q", request.Model, "test-model")
	}
	if len(request.Messages) != 1 {
		t.Fatalf("len(request.Messages) = %d, want 1", len(request.Messages))
	}
	if request.Messages[0].Role != model.RoleUser {
		t.Fatalf("request.Messages[0].Role = %q, want %q", request.Messages[0].Role, model.RoleUser)
	}
	if request.Messages[0].Content != "say hi" {
		t.Fatalf("request.Messages[0].Content = %q, want %q", request.Messages[0].Content, "say hi")
	}
	if len(request.Tools) != 0 {
		t.Fatalf("len(request.Tools) = %d, want 0 for an empty registry", len(request.Tools))
	}
}

func TestInvokeIncludesRegisteredToolDefinitions(t *testing.T) {
	registry := tools.NewToolRegistry()
	// Register out of order to confirm Invoke forwards the sorted definitions.
	registerTool(t, registry, "beta", func(input any, ctx any) (any, error) { return "b", nil })
	registerTool(t, registry, "alpha", func(input any, ctx any) (any, error) { return "a", nil })

	client := &fakeApiClient{script: []scriptedResponse{stopResponse("ok")}}
	a := newTestModel(registry, client)

	if _, err := a.Invoke("hi"); err != nil {
		t.Fatalf("Invoke() error = %v, want nil", err)
	}

	request := client.requestAt(t, 0)
	if len(request.Tools) != 2 {
		t.Fatalf("len(request.Tools) = %d, want 2", len(request.Tools))
	}
	for i, want := range []string{"alpha", "beta"} {
		if got := request.Tools[i].Function.Name; got != want {
			t.Fatalf("request.Tools[%d].Function.Name = %q, want %q", i, got, want)
		}
		if request.Tools[i].Type != "function" {
			t.Fatalf("request.Tools[%d].Type = %q, want %q", i, request.Tools[i].Type, "function")
		}
	}
}

func TestInvokePropagatesApiError(t *testing.T) {
	client := &fakeApiClient{script: []scriptedResponse{{err: eris.New("upstream boom")}}}
	a := newTestModel(nil, client)

	output, err := a.Invoke("hi")
	if err == nil {
		t.Fatal("Invoke() error = nil, want the api error")
	}
	if !strings.Contains(err.Error(), "upstream boom") {
		t.Fatalf("Invoke() error = %v, want it to contain %q", err, "upstream boom")
	}
	if output != "" {
		t.Fatalf("Invoke() = %q, want empty output on error", output)
	}
}

func TestInvokeFailsWhenResponseHasNoChoices(t *testing.T) {
	client := &fakeApiClient{script: []scriptedResponse{{response: model.ChatCompletionResponse{}}}}
	a := newTestModel(nil, client)

	_, err := a.Invoke("hi")
	if err == nil {
		t.Fatal("Invoke() error = nil, want an error for an empty choices list")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Fatalf("Invoke() error = %v, want it to mention %q", err, "no choices")
	}
}

func TestInvokeFailsOnNonStopFinishReasonWithoutToolCalls(t *testing.T) {
	client := &fakeApiClient{script: []scriptedResponse{{
		response: model.ChatCompletionResponse{Choices: []model.Choice{{
			Index:        0,
			Message:      model.NewTextMessage(model.RoleAssistant, "truncated"),
			FinishReason: "length",
		}}},
	}}}
	a := newTestModel(nil, client)

	_, err := a.Invoke("hi")
	if err == nil {
		t.Fatal("Invoke() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), `finished with reason "length"`) {
		t.Fatalf("Invoke() error = %v, want it to mention the finish reason", err)
	}
	if client.callCount() != 1 {
		t.Fatalf("api client calls = %d, want 1", client.callCount())
	}
}

func TestInvokeStopWithToolCallsDoesNotRunTools(t *testing.T) {
	executed := 0

	registry := tools.NewToolRegistry()
	registerTool(t, registry, "echo", func(input any, ctx any) (any, error) {
		executed++
		return "should not run", nil
	})

	client := &fakeApiClient{script: []scriptedResponse{{
		response: model.ChatCompletionResponse{Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:      model.RoleAssistant,
				Content:   "done",
				ToolCalls: []model.ToolCall{newToolCall("call-1", "echo", `{}`)},
			},
			FinishReason: "stop",
		}}},
	}}}
	a := newTestModel(registry, client)

	output, err := a.Invoke("hi")
	if err != nil {
		t.Fatalf("Invoke() error = %v, want nil", err)
	}
	if output != "done" {
		t.Fatalf("Invoke() = %q, want %q", output, "done")
	}
	if executed != 0 {
		t.Fatalf("tool executions = %d, want 0 when finish_reason is stop", executed)
	}
	if client.callCount() != 1 {
		t.Fatalf("api client calls = %d, want 1", client.callCount())
	}
}

func TestInvokeRunsToolCallsUntilStop(t *testing.T) {
	var (
		gotInput   any
		gotContext any
	)

	registry := tools.NewToolRegistry()
	registerTool(t, registry, "echo", func(input any, ctx any) (any, error) {
		gotInput = input
		gotContext = ctx
		return "tool output", nil
	})

	client := &fakeApiClient{script: []scriptedResponse{
		toolCallResponse(newToolCall("call-1", "echo", `{"Text":"hello"}`)),
		stopResponse("final answer"),
	}}
	a := newTestModel(registry, client)

	output, err := a.Invoke("do it")
	if err != nil {
		t.Fatalf("Invoke() error = %v, want nil", err)
	}
	if output != "final answer" {
		t.Fatalf("Invoke() = %q, want %q", output, "final answer")
	}
	if client.callCount() != 2 {
		t.Fatalf("api client calls = %d, want 2", client.callCount())
	}

	if gotInput != `{"Text":"hello"}` {
		t.Fatalf("tool input = %#v, want the raw argument string", gotInput)
	}
	if gotContext != nil {
		t.Fatalf("tool context = %#v, want nil because Invoke no longer supplies one", gotContext)
	}

	second := client.requestAt(t, 1)
	if len(second.Messages) != 3 {
		t.Fatalf("len(second request messages) = %d, want 3", len(second.Messages))
	}

	if second.Messages[0].Role != model.RoleUser || second.Messages[0].Content != "do it" {
		t.Fatalf("messages[0] = %#v, want the original user message", second.Messages[0])
	}

	assistant := second.Messages[1]
	if assistant.Role != model.RoleAssistant {
		t.Fatalf("messages[1].Role = %q, want %q", assistant.Role, model.RoleAssistant)
	}
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].ID != "call-1" {
		t.Fatalf("messages[1].ToolCalls = %#v, want the original tool call", assistant.ToolCalls)
	}

	toolMessage := second.Messages[2]
	if toolMessage.Role != model.RoleTool {
		t.Fatalf("messages[2].Role = %q, want %q", toolMessage.Role, model.RoleTool)
	}
	if toolMessage.ToolCallID != "call-1" {
		t.Fatalf("messages[2].ToolCallID = %q, want %q", toolMessage.ToolCallID, "call-1")
	}

	payload := decodeToolResult(t, toolMessage)
	if !payload.Success {
		t.Fatalf("tool result Success = false, want true (error: %q)", payload.Error)
	}
	if payload.Error != "" {
		t.Fatalf("tool result Error = %q, want empty", payload.Error)
	}
	if payload.Content != "tool output" {
		t.Fatalf("tool result Content = %q, want %q", payload.Content, "tool output")
	}
}

func TestInvokeDefaultsAssistantRoleOnToolCalls(t *testing.T) {
	registry := tools.NewToolRegistry()
	registerTool(t, registry, "echo", func(input any, ctx any) (any, error) { return "ok", nil })

	// Some gateways omit the role on assistant tool-call messages.
	client := &fakeApiClient{script: []scriptedResponse{
		{response: model.ChatCompletionResponse{Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				ToolCalls: []model.ToolCall{newToolCall("call-1", "echo", `{}`)},
			},
			FinishReason: "tool_calls",
		}}}},
		stopResponse("done"),
	}}
	a := newTestModel(registry, client)

	if _, err := a.Invoke("hi"); err != nil {
		t.Fatalf("Invoke() error = %v, want nil", err)
	}

	assistant := client.requestAt(t, 1).Messages[1]
	if assistant.Role != model.RoleAssistant {
		t.Fatalf("assistant message role = %q, want %q", assistant.Role, model.RoleAssistant)
	}
}

func TestInvokeEncodesStructuredToolResults(t *testing.T) {
	registry := tools.NewToolRegistry()
	registerTool(t, registry, "echo", func(input any, ctx any) (any, error) {
		return map[string]any{"echoed": input}, nil
	})

	client := &fakeApiClient{script: []scriptedResponse{
		toolCallResponse(newToolCall("call-1", "echo", `{"Text":"hello"}`)),
		stopResponse("done"),
	}}
	a := newTestModel(registry, client)

	if _, err := a.Invoke("hi"); err != nil {
		t.Fatalf("Invoke() error = %v, want nil", err)
	}

	payload := decodeToolResult(t, client.requestAt(t, 1).Messages[2])
	if !payload.Success {
		t.Fatalf("tool result Success = false, want true (error: %q)", payload.Error)
	}

	var echoed map[string]any
	if err := json.Unmarshal([]byte(payload.Content), &echoed); err != nil {
		t.Fatalf("tool result Content = %q is not JSON: %v", payload.Content, err)
	}
	if echoed["echoed"] != `{"Text":"hello"}` {
		t.Fatalf("tool result Content = %q, want the encoded handler result", payload.Content)
	}
}

func TestInvokeReportsToolErrorAndContinues(t *testing.T) {
	registry := tools.NewToolRegistry()
	registerTool(t, registry, "explode", func(input any, ctx any) (any, error) {
		return nil, eris.New("tool exploded")
	})

	client := &fakeApiClient{script: []scriptedResponse{
		toolCallResponse(newToolCall("call-1", "explode", `{}`)),
		stopResponse("recovered"),
	}}
	a := newTestModel(registry, client)

	output, err := a.Invoke("hi")
	if err != nil {
		t.Fatalf("Invoke() error = %v, want nil; tool errors must be reported to the model", err)
	}
	if output != "recovered" {
		t.Fatalf("Invoke() = %q, want %q", output, "recovered")
	}
	if client.callCount() != 2 {
		t.Fatalf("api client calls = %d, want 2", client.callCount())
	}

	payload := decodeToolResult(t, client.requestAt(t, 1).Messages[2])
	if payload.Success {
		t.Fatal("tool result Success = true, want false for a failing tool")
	}
	if !strings.Contains(payload.Error, "tool exploded") {
		t.Fatalf("tool result Error = %q, want it to contain %q", payload.Error, "tool exploded")
	}
	if payload.Content != "" {
		t.Fatalf("tool result Content = %q, want empty", payload.Content)
	}
}

func TestInvokeReportsUnencodableToolResult(t *testing.T) {
	registry := tools.NewToolRegistry()
	registerTool(t, registry, "channel", func(input any, ctx any) (any, error) {
		return make(chan int), nil
	})

	client := &fakeApiClient{script: []scriptedResponse{
		toolCallResponse(newToolCall("call-1", "channel", `{}`)),
		stopResponse("done"),
	}}
	a := newTestModel(registry, client)

	if _, err := a.Invoke("hi"); err != nil {
		t.Fatalf("Invoke() error = %v, want nil; encoding failures must be reported to the model", err)
	}

	payload := decodeToolResult(t, client.requestAt(t, 1).Messages[2])
	if payload.Success {
		t.Fatal("tool result Success = true, want false when the result cannot be encoded")
	}
	if !strings.Contains(payload.Error, "failed to encode tool result") {
		t.Fatalf("tool result Error = %q, want it to mention %q", payload.Error, "failed to encode tool result")
	}
}

func TestInvokeFailsForUnknownTool(t *testing.T) {
	client := &fakeApiClient{script: []scriptedResponse{
		toolCallResponse(newToolCall("call-1", "missing", `{}`)),
	}}
	a := newTestModel(nil, client)

	output, err := a.Invoke("hi")
	if err == nil {
		t.Fatal("Invoke() error = nil, want an error for an unknown tool")
	}
	if !strings.Contains(err.Error(), `tool "missing" not found`) {
		t.Fatalf("Invoke() error = %v, want it to mention the unknown tool", err)
	}
	if output != "" {
		t.Fatalf("Invoke() = %q, want empty output on error", output)
	}
	if client.callCount() != 1 {
		t.Fatalf("api client calls = %d, want 1", client.callCount())
	}
}

func TestInvokeStopsAfterMaxToolRounds(t *testing.T) {
	registry := tools.NewToolRegistry()
	registerTool(t, registry, "loop", func(input any, ctx any) (any, error) { return "again", nil })

	client := &fakeApiClient{}
	for i := 0; i < maxToolRounds*2; i++ {
		client.script = append(client.script, toolCallResponse(newToolCall(fmt.Sprintf("call-%d", i), "loop", `{}`)))
	}
	a := newTestModel(registry, client)

	_, err := a.Invoke("loop forever")
	if err == nil {
		t.Fatal("Invoke() error = nil, want a round limit error")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("exceeded the limit of %d tool rounds", maxToolRounds)) {
		t.Fatalf("Invoke() error = %v, want the round limit error", err)
	}
	if client.callCount() != maxToolRounds {
		t.Fatalf("api client calls = %d, want %d", client.callCount(), maxToolRounds)
	}
}

// ==========================================================================
// runToolCall
// ==========================================================================

func TestRunToolCallUnknownTool(t *testing.T) {
	a := newTestModel(nil, &fakeApiClient{})

	_, err := a.runToolCall(newToolCall("call-1", "missing", `{}`), nil)
	if err == nil {
		t.Fatal("runToolCall() error = nil, want an unknown tool error")
	}
	if !strings.Contains(err.Error(), `tool "missing" not found`) {
		t.Fatalf("runToolCall() error = %v, want it to mention the unknown tool", err)
	}
}

func TestRunToolCallSuccess(t *testing.T) {
	var gotContext any

	registry := tools.NewToolRegistry()
	registerTool(t, registry, "echo", func(input any, ctx any) (any, error) {
		gotContext = ctx
		return "result", nil
	})
	a := newTestModel(registry, &fakeApiClient{})

	ctx := "context-value"
	message, err := a.runToolCall(newToolCall("call-1", "echo", `{"Text":"x"}`), ctx)
	if err != nil {
		t.Fatalf("runToolCall() error = %v, want nil", err)
	}
	if message.Role != model.RoleTool {
		t.Fatalf("message.Role = %q, want %q", message.Role, model.RoleTool)
	}
	if message.ToolCallID != "call-1" {
		t.Fatalf("message.ToolCallID = %q, want %q", message.ToolCallID, "call-1")
	}
	if gotContext != ctx {
		t.Fatalf("tool context = %#v, want %#v", gotContext, ctx)
	}

	payload := decodeToolResult(t, message)
	if !payload.Success || payload.Error != "" || payload.Content != "result" {
		t.Fatalf("tool result = %#v, want {Success:true Error:\"\" Content:\"result\"}", payload)
	}
}

func TestRunToolCallNilResult(t *testing.T) {
	registry := tools.NewToolRegistry()
	registerTool(t, registry, "noop", func(input any, ctx any) (any, error) { return nil, nil })
	a := newTestModel(registry, &fakeApiClient{})

	message, err := a.runToolCall(newToolCall("call-1", "noop", `{}`), nil)
	if err != nil {
		t.Fatalf("runToolCall() error = %v, want nil", err)
	}

	payload := decodeToolResult(t, message)
	if !payload.Success {
		t.Fatalf("tool result Success = false, want true (error: %q)", payload.Error)
	}
	if payload.Content != "" {
		t.Fatalf("tool result Content = %q, want empty", payload.Content)
	}
}

func TestRunToolCallToolError(t *testing.T) {
	registry := tools.NewToolRegistry()
	registerTool(t, registry, "explode", func(input any, ctx any) (any, error) {
		return nil, eris.New("kaboom")
	})
	a := newTestModel(registry, &fakeApiClient{})

	message, err := a.runToolCall(newToolCall("call-1", "explode", `{}`), nil)
	if err != nil {
		t.Fatalf("runToolCall() error = %v, want nil; the tool error belongs in the payload", err)
	}

	payload := decodeToolResult(t, message)
	if payload.Success {
		t.Fatal("tool result Success = true, want false")
	}
	if !strings.Contains(payload.Error, "kaboom") {
		t.Fatalf("tool result Error = %q, want it to contain %q", payload.Error, "kaboom")
	}
}

func TestRunToolCallResultWithError(t *testing.T) {
	registry := tools.NewToolRegistry()
	registerTool(t, registry, "partial", func(input any, ctx any) (any, error) {
		return "partial output", eris.New("partial failure")
	})
	a := newTestModel(registry, &fakeApiClient{})

	message, err := a.runToolCall(newToolCall("call-1", "partial", `{}`), nil)
	if err != nil {
		t.Fatalf("runToolCall() error = %v, want nil", err)
	}

	payload := decodeToolResult(t, message)
	if payload.Success {
		t.Fatal("tool result Success = true, want false")
	}
	if !strings.Contains(payload.Error, "partial failure") {
		t.Fatalf("tool result Error = %q, want it to contain %q", payload.Error, "partial failure")
	}
	if payload.Content != "partial output" {
		t.Fatalf("tool result Content = %q, want %q", payload.Content, "partial output")
	}
}

// ==========================================================================
// encodeToolResult
// ==========================================================================

func TestEncodeToolResult(t *testing.T) {
	tests := []struct {
		name    string
		result  any
		want    string
		wantErr bool
	}{
		{name: "string passes through unchanged", result: "already text", want: "already text"},
		{name: "empty string passes through", result: "", want: ""},
		{name: "map is json encoded", result: map[string]any{"a": 1}, want: `{"a":1}`},
		{name: "slice is json encoded", result: []int{1, 2}, want: `[1,2]`},
		{name: "number is json encoded", result: 42, want: "42"},
		{name: "nil is json encoded", result: nil, want: "null"},
		{name: "struct is json encoded", result: struct {
			Name string `json:"name"`
		}{Name: "x"}, want: `{"name":"x"}`},
		{name: "unencodable value returns an error", result: make(chan int), wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := encodeToolResult(tc.result)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("encodeToolResult() error = nil, want an error")
				}
				if !strings.Contains(err.Error(), "failed to encode tool result") {
					t.Fatalf("encodeToolResult() error = %v, want it to mention %q", err, "failed to encode tool result")
				}
				return
			}

			if err != nil {
				t.Fatalf("encodeToolResult() error = %v, want nil", err)
			}
			if got != tc.want {
				t.Fatalf("encodeToolResult() = %q, want %q", got, tc.want)
			}
		})
	}
}
