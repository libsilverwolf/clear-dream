package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libsilverwolf/clear-dream/model"
)

// fakeTool is a minimal Tool implementation used to exercise the registry and
// the definition builder.
type fakeTool struct {
	description string
	params      []ToolArgument
	runFn       func(any, any) (any, error)
}

func (f *fakeTool) Description() string { return f.description }

func (f *fakeTool) GetParameters() []ToolArgument { return f.params }

func (f *fakeTool) Run(params any, context any) (any, error) {
	if f.runFn != nil {
		return f.runFn(params, context)
	}
	return params, nil
}

func newFakeTool(description string, params ...ToolArgument) *fakeTool {
	return &fakeTool{description: description, params: params}
}

func TestRegisterAndGetTool(t *testing.T) {
	registry := NewToolRegistry()
	tool := newFakeTool("repeats input")

	registry.RegisterTool("echo", tool)
	if !registry.HasTool("echo") {
		t.Fatalf("HasTool(%q) = false after RegisterTool, want true", "echo")
	}

	got, ok := registry.GetTool("echo")
	if !ok {
		t.Fatalf("GetTool(%q) ok = false, want true", "echo")
	}
	if got != Tool(tool) {
		t.Fatalf("GetTool(%q) = %#v, want the registered tool instance", "echo", got)
	}
}

func TestGetToolUnregistered(t *testing.T) {
	registry := NewToolRegistry()

	got, ok := registry.GetTool("missing")
	if ok {
		t.Fatalf("GetTool(%q) ok = true, want false", "missing")
	}
	if got != nil {
		t.Fatalf("GetTool(%q) = %#v, want nil", "missing", got)
	}

	if registry.HasTool("missing") {
		t.Fatalf("HasTool(%q) = true, want false", "missing")
	}
}

func TestRegisterToolOverwrites(t *testing.T) {
	registry := NewToolRegistry()
	first := newFakeTool("first")
	second := newFakeTool("second")

	registry.RegisterTool("dup", first)
	registry.RegisterTool("dup", second)

	got, ok := registry.GetTool("dup")
	if !ok {
		t.Fatalf("GetTool(%q) ok = false, want true", "dup")
	}
	if got != Tool(second) {
		t.Fatalf("GetTool(%q) = %#v, want the second registered tool", "dup", got)
	}
	if desc := got.Description(); desc != "second" {
		t.Fatalf("Description() = %q, want %q", desc, "second")
	}
}

func TestGetToolDefinition(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterTool("echo", newFakeTool("repeats input",
		ToolArgument{Name: "text", Type: "string", Description: "text to repeat"},
	))

	definition, ok := registry.GetToolDefinition("echo")
	if !ok {
		t.Fatalf("GetToolDefinition(%q) ok = false, want true", "echo")
	}

	want := model.Tool{
		Type: "function",
		Function: model.FunctionSchema{
			Name:        "echo",
			Description: "repeats input",
			Parameters: model.JSONSchema{
				Type: "object",
				Properties: map[string]model.JSONSchema{
					"text": {Type: "string", Description: "text to repeat"},
				},
				Required: []string{"text"},
			},
		},
	}
	if !reflect.DeepEqual(definition, want) {
		t.Fatalf("GetToolDefinition(%q) = %#v, want %#v", "echo", definition, want)
	}
}

func TestGetToolDefinitionUnregistered(t *testing.T) {
	registry := NewToolRegistry()

	definition, ok := registry.GetToolDefinition("missing")
	if ok {
		t.Fatalf("GetToolDefinition(%q) ok = true, want false", "missing")
	}
	if !reflect.DeepEqual(definition, model.Tool{}) {
		t.Fatalf("GetToolDefinition(%q) = %#v, want zero value", "missing", definition)
	}
}

func TestGetToolDefinitionsSorted(t *testing.T) {
	registry := NewToolRegistry()
	// Register out of alphabetical order to verify deterministic output.
	for _, name := range []string{"charlie", "alpha", "bravo"} {
		registry.RegisterTool(name, newFakeTool("desc "+name))
	}

	definitions := registry.GetToolDefinitions()
	if len(definitions) != 3 {
		t.Fatalf("len(GetToolDefinitions()) = %d, want 3", len(definitions))
	}

	wantNames := []string{"alpha", "bravo", "charlie"}
	for i, want := range wantNames {
		if got := definitions[i].Function.Name; got != want {
			t.Fatalf("definitions[%d].Function.Name = %q, want %q", i, got, want)
		}
		if got, wantDesc := definitions[i].Function.Description, "desc "+want; got != wantDesc {
			t.Fatalf("definitions[%d].Function.Description = %q, want %q", i, got, wantDesc)
		}
		if definitions[i].Type != "function" {
			t.Fatalf("definitions[%d].Type = %q, want %q", i, definitions[i].Type, "function")
		}
	}
}

func TestGetToolDefinitionsEmpty(t *testing.T) {
	definitions := NewToolRegistry().GetToolDefinitions()
	if definitions == nil {
		t.Fatal("GetToolDefinitions() = nil, want empty non-nil slice")
	}
	if len(definitions) != 0 {
		t.Fatalf("len(GetToolDefinitions()) = %d, want 0", len(definitions))
	}
}

func TestBuildToolDefinition(t *testing.T) {
	tests := []struct {
		name       string
		toolName   string
		tool       Tool
		want       model.Tool
		wantNoArgs bool
	}{
		{
			name:     "nil tool yields zero definition",
			toolName: "nil",
			tool:     nil,
			want:     model.Tool{},
		},
		{
			name:     "no parameters leaves required nil",
			toolName: "noargs",
			tool:     newFakeTool("does nothing"),
			want: model.Tool{
				Type: "function",
				Function: model.FunctionSchema{
					Name:        "noargs",
					Description: "does nothing",
					Parameters:  model.JSONSchema{Type: "object", Properties: map[string]model.JSONSchema{}},
				},
			},
		},
		{
			name:     "multiple parameters are all required and keep their JSON types",
			toolName: "multi",
			tool: newFakeTool("multi arg tool",
				ToolArgument{Name: "a", Type: "integer", Description: "first"},
				ToolArgument{Name: "b", Type: "boolean", Description: "second"},
			),
			want: model.Tool{
				Type: "function",
				Function: model.FunctionSchema{
					Name:        "multi",
					Description: "multi arg tool",
					Parameters: model.JSONSchema{
						Type: "object",
						Properties: map[string]model.JSONSchema{
							"a": {Type: "integer", Description: "first"},
							"b": {Type: "boolean", Description: "second"},
						},
						Required: []string{"a", "b"},
					},
				},
			},
		},
		{
			name:     "parameters with empty name are skipped",
			toolName: "skip",
			tool: newFakeTool("skips unnamed",
				ToolArgument{Name: "", Type: "string", Description: "ignored"},
				ToolArgument{Name: "kept", Description: "kept param"},
			),
			want: model.Tool{
				Type: "function",
				Function: model.FunctionSchema{
					Name:        "skip",
					Description: "skips unnamed",
					Parameters: model.JSONSchema{
						Type: "object",
						Properties: map[string]model.JSONSchema{
							"kept": {Type: "string", Description: "kept param"},
						},
						Required: []string{"kept"},
					},
				},
			},
		},
		{
			name:     "all parameters unnamed yields no required list",
			toolName: "allunnamed",
			tool: newFakeTool("all unnamed",
				ToolArgument{Name: "", Description: "ignored"},
			),
			want: model.Tool{
				Type: "function",
				Function: model.FunctionSchema{
					Name:        "allunnamed",
					Description: "all unnamed",
					Parameters:  model.JSONSchema{Type: "object", Properties: map[string]model.JSONSchema{}},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildToolDefinition(tc.toolName, tc.tool)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("BuildToolDefinition(%q) = %#v, want %#v", tc.toolName, got, tc.want)
			}
			if tc.wantNoArgs && got.Function.Parameters.Required != nil {
				t.Fatalf("Required = %#v, want nil", got.Function.Parameters.Required)
			}
		})
	}
}

func TestBuildToolDefinitionNilTool(t *testing.T) {
	if got := BuildToolDefinition("nil", nil); !reflect.DeepEqual(got, model.Tool{}) {
		t.Fatalf("BuildToolDefinition(nil) = %#v, want zero value", got)
	}
}

func TestToolRun(t *testing.T) {
	tool := newFakeTool("adds suffix", ToolArgument{Name: "suffix"})
	tool.runFn = func(params any, context any) (any, error) {
		return fmt.Sprintf("%v%v", params, context), nil
	}

	got, err := tool.Run("hello", " world")
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if got != "hello world" {
		t.Fatalf("Run() = %v, want %q", got, "hello world")
	}
}

func TestToolRunNilCallbackReturnsParams(t *testing.T) {
	tool := newFakeTool("passthrough")

	got, err := tool.Run(map[string]any{"k": "v"}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if !reflect.DeepEqual(got, map[string]any{"k": "v"}) {
		t.Fatalf("Run() = %#v, want params passed through", got)
	}
}

func TestToolRegistryConcurrentAccess(t *testing.T) {
	registry := NewToolRegistry()
	const workers = 16
	const perWorker = 25

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				name := fmt.Sprintf("tool-%d-%d", w, i)
				registry.RegisterTool(name, newFakeTool(name))

				if !registry.HasTool(name) {
					t.Errorf("HasTool(%q) = false right after registration", name)
					return
				}
				if _, ok := registry.GetTool(name); !ok {
					t.Errorf("GetTool(%q) ok = false", name)
					return
				}
				if _, ok := registry.GetToolDefinition(name); !ok {
					t.Errorf("GetToolDefinition(%q) ok = false", name)
					return
				}
				_ = registry.GetToolDefinitions()
			}
		}(w)
	}
	wg.Wait()

	definitions := registry.GetToolDefinitions()
	if len(definitions) != workers*perWorker {
		t.Fatalf("len(GetToolDefinitions()) = %d, want %d", len(definitions), workers*perWorker)
	}
}

func boolPtr(v bool) *bool { return &v }

type customStruct struct {
	Key string
}

type schemaParams struct {
	Name     string `description:"name of the thing"`
	Count    int    `description:"how many"`
	Ratio    float64
	Enabled  bool
	Optional *string
	Tags     []string
	Matrix   [][]int
	Fixed    [2]int
	Limits   map[string]int
	Meta     customStruct
	When     time.Time
	Anything any
}

func TestBuildToolDefinitionMapsTypeNames(t *testing.T) {
	tests := []struct {
		name    string
		argType string
		want    model.JSONSchema
	}{
		{name: "go bool", argType: "bool", want: model.JSONSchema{Type: "boolean"}},
		{name: "json boolean", argType: "boolean", want: model.JSONSchema{Type: "boolean"}},
		{name: "go string", argType: "string", want: model.JSONSchema{Type: "string"}},
		{name: "go int", argType: "int", want: model.JSONSchema{Type: "integer"}},
		{name: "go uint64", argType: "uint64", want: model.JSONSchema{Type: "integer"}},
		{name: "byte alias", argType: "byte", want: model.JSONSchema{Type: "integer"}},
		{name: "rune alias", argType: "rune", want: model.JSONSchema{Type: "integer"}},
		{name: "go float32", argType: "float32", want: model.JSONSchema{Type: "number"}},
		{name: "go float64", argType: "float64", want: model.JSONSchema{Type: "number"}},
		{name: "json number", argType: "number", want: model.JSONSchema{Type: "number"}},
		{name: "pointer", argType: "*bool", want: model.JSONSchema{Type: "boolean"}},
		{
			name:    "slice",
			argType: "[]string",
			want:    model.JSONSchema{Type: "array", Items: &model.JSONSchema{Type: "string"}},
		},
		{
			name:    "nested slice",
			argType: "[][]int",
			want: model.JSONSchema{Type: "array", Items: &model.JSONSchema{
				Type:  "array",
				Items: &model.JSONSchema{Type: "integer"},
			}},
		},
		{
			name:    "fixed-size array",
			argType: "[3]float64",
			want:    model.JSONSchema{Type: "array", Items: &model.JSONSchema{Type: "number"}},
		},
		{
			name:    "map",
			argType: "map[string]int",
			want:    model.JSONSchema{Type: "object", AdditionalProperties: boolPtr(true)},
		},
		{name: "object", argType: "object", want: model.JSONSchema{Type: "object"}},
		{name: "array", argType: "array", want: model.JSONSchema{Type: "array"}},
		{name: "empty type", argType: "", want: model.JSONSchema{Type: "string"}},
		{name: "named type", argType: "pkg.Custom", want: model.JSONSchema{Type: "string"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tool := newFakeTool("describes a value",
				ToolArgument{Name: "value", Type: tc.argType, Description: "the value"},
			)

			definition := BuildToolDefinition("tool", tool)
			got, ok := definition.Function.Parameters.Properties["value"]
			if !ok {
				t.Fatalf("Properties missing key %q", "value")
			}

			tc.want.Description = "the value"
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Properties[%q] = %#v, want %#v", "value", got, tc.want)
			}
		})
	}
}

func TestBuildToolDefinitionMapsReflectedGoTypes(t *testing.T) {
	tool, err := CreateTool(CreateToolParams{
		Name:        "schema",
		Description: "exercises type mapping",
		Handler:     func(params any, context any) (any, error) { return nil, nil },
		ParamStruct: schemaParams{},
	})
	if err != nil {
		t.Fatalf("CreateTool() error = %v, want nil", err)
	}

	properties := BuildToolDefinition("schema", tool).Function.Parameters.Properties

	want := map[string]model.JSONSchema{
		"Name":     {Type: "string", Description: "name of the thing"},
		"Count":    {Type: "integer", Description: "how many"},
		"Ratio":    {Type: "number"},
		"Enabled":  {Type: "boolean"},
		"Optional": {Type: "string"},
		"Tags":     {Type: "array", Items: &model.JSONSchema{Type: "string"}},
		"Matrix": {Type: "array", Items: &model.JSONSchema{
			Type:  "array",
			Items: &model.JSONSchema{Type: "integer"},
		}},
		"Fixed":    {Type: "array", Items: &model.JSONSchema{Type: "integer"}},
		"Limits":   {Type: "object", AdditionalProperties: boolPtr(true)},
		"Meta":     {Type: "object"},
		"When":     {Type: "string"},
		"Anything": {Type: "string"},
	}

	if len(properties) != len(want) {
		t.Fatalf("len(Properties) = %d, want %d", len(properties), len(want))
	}
	for name, wantSchema := range want {
		got, ok := properties[name]
		if !ok {
			t.Fatalf("Properties missing key %q", name)
		}
		if !reflect.DeepEqual(got, wantSchema) {
			t.Fatalf("Properties[%q] = %#v, want %#v", name, got, wantSchema)
		}
	}
}

// decodeRunResult decodes the ToolRunResult envelope returned by RunTool.
func decodeRunResult(t *testing.T, content string) ToolRunResult {
	t.Helper()

	var result ToolRunResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatalf("decode RunTool() content %q: %v", content, err)
	}

	return result
}

func TestRegistryRunTool(t *testing.T) {
	registry := NewToolRegistry()

	var gotParams, gotContext any
	tool := newFakeTool("echoes its input")
	tool.runFn = func(params any, ctx any) (any, error) {
		gotParams, gotContext = params, ctx
		return "hello " + params.(string), nil
	}
	registry.RegisterTool("echo", tool)

	content, err := registry.RunTool(model.ToolCall{
		ID: "call-1",
		Function: model.FunctionCallData{
			Name:      "echo",
			Arguments: "world",
		},
	}, "ctx-value")
	if err != nil {
		t.Fatalf("RunTool() error = %v, want nil", err)
	}

	outcome := decodeRunResult(t, content)
	if !outcome.Success {
		t.Fatalf("RunTool() Success = false, want true (error: %q)", outcome.Error)
	}
	if outcome.Error != "" {
		t.Fatalf("RunTool() Error = %q, want empty", outcome.Error)
	}
	if outcome.Data != `"hello world"` {
		t.Fatalf("RunTool() Data = %q, want %q", outcome.Data, `"hello world"`)
	}
	if gotParams != "world" {
		t.Fatalf("tool params = %#v, want %q", gotParams, "world")
	}
	if gotContext != "ctx-value" {
		t.Fatalf("tool context = %#v, want %q", gotContext, "ctx-value")
	}
}

func TestRegistryRunToolEncodesResults(t *testing.T) {
	tests := []struct {
		name       string
		result     any
		wantData   string
		wantError  string
		wantFailed bool
	}{
		{name: "string is json encoded", result: "already text", wantData: `"already text"`},
		{name: "empty string is json encoded", result: "", wantData: `""`},
		{name: "map is json encoded", result: map[string]any{"a": 1}, wantData: `{"a":1}`},
		{name: "slice is json encoded", result: []int{1, 2}, wantData: `[1,2]`},
		{name: "number is json encoded", result: 42, wantData: "42"},
		{name: "nil result leaves data empty", result: nil},
		{name: "struct is json encoded", result: struct {
			Name string `json:"name"`
		}{Name: "x"}, wantData: `{"name":"x"}`},
		{name: "unencodable value is reported", result: make(chan int), wantFailed: true, wantError: "failed to encode tool result"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewToolRegistry()
			result := tc.result
			tool := newFakeTool("returns a result")
			tool.runFn = func(params any, ctx any) (any, error) {
				return result, nil
			}
			registry.RegisterTool("result", tool)

			content, err := registry.RunTool(model.ToolCall{
				Function: model.FunctionCallData{Name: "result"},
			}, nil)
			if err != nil {
				t.Fatalf("RunTool() error = %v, want nil", err)
			}

			outcome := decodeRunResult(t, content)
			if outcome.Data != tc.wantData {
				t.Fatalf("RunTool() Data = %q, want %q", outcome.Data, tc.wantData)
			}
			if tc.wantFailed {
				if outcome.Success {
					t.Fatal("RunTool() Success = true, want false")
				}
				if !strings.Contains(outcome.Error, tc.wantError) {
					t.Fatalf("RunTool() Error = %q, want it to contain %q", outcome.Error, tc.wantError)
				}
				return
			}
			if !outcome.Success {
				t.Fatalf("RunTool() Success = false, want true (error: %q)", outcome.Error)
			}
			if outcome.Error != "" {
				t.Fatalf("RunTool() Error = %q, want empty", outcome.Error)
			}
		})
	}
}

func TestRegistryRunToolUnknownTool(t *testing.T) {
	content, err := NewToolRegistry().RunTool(model.ToolCall{
		Function: model.FunctionCallData{Name: "missing"},
	}, nil)
	if err == nil {
		t.Fatal("RunTool() error = nil, want an unknown tool error")
	}
	if !strings.Contains(err.Error(), `tool "missing" not found`) {
		t.Fatalf("RunTool() error = %v, want it to mention the unknown tool", err)
	}
	if content != "" {
		t.Fatalf("RunTool() content = %q, want empty", content)
	}
}

func TestRegistryRunToolReturnsResultWithError(t *testing.T) {
	registry := NewToolRegistry()
	tool := newFakeTool("fails after producing a result")
	tool.runFn = func(params any, ctx any) (any, error) {
		return "partial output", errors.New("boom")
	}
	registry.RegisterTool("partial", tool)

	content, err := registry.RunTool(model.ToolCall{
		Function: model.FunctionCallData{Name: "partial"},
	}, nil)
	if err == nil {
		t.Fatal("RunTool() error = nil, want the tool error")
	}

	outcome := decodeRunResult(t, content)
	if outcome.Success {
		t.Fatal("RunTool() Success = true, want false")
	}
	if !strings.Contains(outcome.Error, "boom") {
		t.Fatalf("RunTool() Error = %q, want it to contain %q", outcome.Error, "boom")
	}
	if outcome.Data != `"partial output"` {
		t.Fatalf("RunTool() Data = %q, want %q", outcome.Data, `"partial output"`)
	}
}

func TestRegistryRunToolNilResultWithError(t *testing.T) {
	registry := NewToolRegistry()
	tool := newFakeTool("fails")
	tool.runFn = func(params any, ctx any) (any, error) {
		return nil, errors.New("boom")
	}
	registry.RegisterTool("failing", tool)

	content, err := registry.RunTool(model.ToolCall{
		Function: model.FunctionCallData{Name: "failing"},
	}, nil)
	if err == nil {
		t.Fatal("RunTool() error = nil, want the tool error")
	}

	outcome := decodeRunResult(t, content)
	if outcome.Success {
		t.Fatal("RunTool() Success = true, want false")
	}
	if outcome.Error != "boom" {
		t.Fatalf("RunTool() Error = %q, want %q", outcome.Error, "boom")
	}
	if outcome.Data != "" {
		t.Fatalf("RunTool() Data = %q, want empty", outcome.Data)
	}
}
