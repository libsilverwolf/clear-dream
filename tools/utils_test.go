package tools

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// weatherParams is a representative parameter struct: exported fields with
// description tags, one untagged field, and a mix of primitive types.
type weatherParams struct {
	City     string `description:"city name"`
	Days     int    `description:"forecast days"`
	Verbose  bool   `description:"verbose output"`
	Untagged float64
}

// emptyParams has no fields at all.
type emptyParams struct{}

func TestCreateToolValidation(t *testing.T) {
	handler := func(params any, context any) (any, error) { return nil, nil }

	tests := []struct {
		name    string
		params  CreateToolParams
		wantErr string
	}{
		{
			name:    "empty name",
			params:  CreateToolParams{Description: "d", Handler: handler, ParamStruct: emptyParams{}},
			wantErr: "tool name cannot be empty",
		},
		{
			name:    "empty description",
			params:  CreateToolParams{Name: "n", Handler: handler, ParamStruct: emptyParams{}},
			wantErr: "tool description cannot be empty",
		},
		{
			name:    "nil handler",
			params:  CreateToolParams{Name: "n", Description: "d", ParamStruct: emptyParams{}},
			wantErr: "tool handler cannot be nil",
		},
		{
			name:    "zero value params",
			params:  CreateToolParams{},
			wantErr: "tool name cannot be empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tool, err := CreateTool(tc.params)
			if err == nil {
				t.Fatalf("CreateTool() error = nil, want %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("CreateTool() error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
			if tool != nil {
				t.Fatalf("CreateTool() tool = %#v, want nil on error", tool)
			}
		})
	}
}

func TestCreateToolSuccess(t *testing.T) {
	var gotParams, gotContext any
	handler := func(params any, context any) (any, error) {
		gotParams, gotContext = params, context
		return "forecast", nil
	}

	tool, err := CreateTool(CreateToolParams{
		Name:        "weather",
		Description: "looks up the weather",
		Handler:     handler,
		ParamStruct: weatherParams{},
	})
	if err != nil {
		t.Fatalf("CreateTool() error = %v, want nil", err)
	}
	if tool == nil {
		t.Fatal("CreateTool() tool = nil, want non-nil")
	}

	if got, want := tool.Description(), "looks up the weather"; got != want {
		t.Fatalf("Description() = %q, want %q", got, want)
	}

	wantArgs := []ToolArgument{
		{Name: "City", Type: "string", Description: "city name", goType: reflect.TypeOf("")},
		{Name: "Days", Type: "int", Description: "forecast days", goType: reflect.TypeOf(0)},
		{Name: "Verbose", Type: "bool", Description: "verbose output", goType: reflect.TypeOf(false)},
		{Name: "Untagged", Type: "float64", Description: "", goType: reflect.TypeOf(0.0)},
	}
	if got := tool.GetParameters(); !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("GetParameters() = %#v, want %#v", got, wantArgs)
	}

	result, err := tool.Run(map[string]any{"City": "Paris"}, "ctx")
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if result != "forecast" {
		t.Fatalf("Run() = %#v, want %q", result, "forecast")
	}
	if !reflect.DeepEqual(gotParams, map[string]any{"City": "Paris"}) {
		t.Fatalf("handler params = %#v, want the params passed to Run", gotParams)
	}
	if gotContext != "ctx" {
		t.Fatalf("handler context = %#v, want %q", gotContext, "ctx")
	}
}

func TestCreateToolPropagatesHandlerError(t *testing.T) {
	wantErr := errors.New("boom")
	tool, err := CreateTool(CreateToolParams{
		Name:        "failing",
		Description: "always fails",
		Handler: func(params any, context any) (any, error) {
			return nil, wantErr
		},
		ParamStruct: weatherParams{},
	})
	if err != nil {
		t.Fatalf("CreateTool() error = %v, want nil", err)
	}

	_, err = tool.Run(nil, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want %v", err, wantErr)
	}
}

func TestCreateToolNoParams(t *testing.T) {
	tests := []struct {
		name        string
		paramStruct any
	}{
		{name: "nil param struct", paramStruct: nil},
		{name: "empty struct", paramStruct: emptyParams{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tool, err := CreateTool(CreateToolParams{
				Name:        "noargs",
				Description: "takes no arguments",
				Handler:     func(params any, context any) (any, error) { return nil, nil },
				ParamStruct: tc.paramStruct,
			})
			if err != nil {
				t.Fatalf("CreateTool() error = %v, want nil", err)
			}
			if got := tool.GetParameters(); len(got) != 0 {
				t.Fatalf("GetParameters() = %#v, want empty", got)
			}
		})
	}
}

func TestCreateToolInvalidParamStruct(t *testing.T) {
	tests := []struct {
		name        string
		paramStruct any
	}{
		{name: "int", paramStruct: 42},
		{name: "string", paramStruct: "params"},
		{name: "pointer to struct", paramStruct: &weatherParams{}},
		{name: "slice", paramStruct: []string{"a"}},
		{name: "map", paramStruct: map[string]string{"a": "b"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tool, err := CreateTool(CreateToolParams{
				Name:        "bad",
				Description: "bad params",
				Handler:     func(params any, context any) (any, error) { return nil, nil },
				ParamStruct: tc.paramStruct,
			})
			if err == nil {
				t.Fatalf("CreateTool() error = nil, want error for %T", tc.paramStruct)
			}
			if !strings.Contains(err.Error(), "failed to extract tool parameters") {
				t.Fatalf("CreateTool() error = %q, want it to be wrapped with %q", err.Error(), "failed to extract tool parameters")
			}
			if tool != nil {
				t.Fatalf("CreateTool() tool = %#v, want nil on error", tool)
			}
		})
	}
}

func TestExtractToolParams(t *testing.T) {
	args, err := ExtractToolParams(weatherParams{})
	if err != nil {
		t.Fatalf("ExtractToolParams() error = %v, want nil", err)
	}

	want := []ToolArgument{
		{Name: "City", Type: "string", Description: "city name", goType: reflect.TypeOf("")},
		{Name: "Days", Type: "int", Description: "forecast days", goType: reflect.TypeOf(0)},
		{Name: "Verbose", Type: "bool", Description: "verbose output", goType: reflect.TypeOf(false)},
		{Name: "Untagged", Type: "float64", Description: "", goType: reflect.TypeOf(0.0)},
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("ExtractToolParams() = %#v, want %#v", args, want)
	}
}

func TestExtractToolParamsFieldOrder(t *testing.T) {
	type ordered struct {
		First  string
		Second string
		Third  string
	}

	args, err := ExtractToolParams(ordered{})
	if err != nil {
		t.Fatalf("ExtractToolParams() error = %v, want nil", err)
	}
	if len(args) != 3 {
		t.Fatalf("len(ExtractToolParams()) = %d, want 3", len(args))
	}

	wantNames := []string{"First", "Second", "Third"}
	for i, want := range wantNames {
		if args[i].Name != want {
			t.Fatalf("args[%d].Name = %q, want %q", i, args[i].Name, want)
		}
	}
}

func TestExtractToolParamsNil(t *testing.T) {
	args, err := ExtractToolParams(nil)
	if err != nil {
		t.Fatalf("ExtractToolParams(nil) error = %v, want nil", err)
	}
	if len(args) != 0 {
		t.Fatalf("ExtractToolParams(nil) = %#v, want empty", args)
	}
}

func TestExtractToolParamsNonStruct(t *testing.T) {
	tests := []struct {
		name     string
		param    any
		wantKind string
	}{
		{name: "int", param: 1, wantKind: "int"},
		{name: "string", param: "s", wantKind: "string"},
		{name: "pointer", param: &weatherParams{}, wantKind: "ptr"},
		{name: "slice", param: []int{1}, wantKind: "slice"},
		{name: "func", param: func() {}, wantKind: "func"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args, err := ExtractToolParams(tc.param)
			if err == nil {
				t.Fatalf("ExtractToolParams(%T) error = nil, want error", tc.param)
			}
			if !strings.Contains(err.Error(), "expected struct type") {
				t.Fatalf("ExtractToolParams(%T) error = %q, want it to mention %q", tc.param, err.Error(), "expected struct type")
			}
			if !strings.Contains(err.Error(), tc.wantKind) {
				t.Fatalf("ExtractToolParams(%T) error = %q, want it to mention kind %q", tc.param, err.Error(), tc.wantKind)
			}
			if args != nil {
				t.Fatalf("ExtractToolParams(%T) = %#v, want nil on error", tc.param, args)
			}
		})
	}
}

// TestCreatedToolImplementsTool ensures CreateTool returns a value satisfying the
// Tool interface and that its definitions feed the registry and schema builder.
func TestCreatedToolIntegratesWithRegistry(t *testing.T) {
	tool, err := CreateTool(CreateToolParams{
		Name:        "weather",
		Description: "looks up the weather",
		Handler:     func(params any, context any) (any, error) { return "sunny", nil },
		ParamStruct: weatherParams{},
	})
	if err != nil {
		t.Fatalf("CreateTool() error = %v, want nil", err)
	}

	registry := NewToolRegistry()
	registry.RegisterTool("weather", tool)

	definition, ok := registry.GetToolDefinition("weather")
	if !ok {
		t.Fatal("GetToolDefinition() ok = false, want true")
	}

	fn := definition.Function
	if fn.Name != "weather" {
		t.Fatalf("Function.Name = %q, want %q", fn.Name, "weather")
	}
	if fn.Description != "looks up the weather" {
		t.Fatalf("Function.Description = %q, want %q", fn.Description, "looks up the weather")
	}
	if got, want := len(fn.Parameters.Properties), len(tool.GetParameters()); got != want {
		t.Fatalf("len(Parameters.Properties) = %d, want %d", got, want)
	}
	wantTypes := map[string]string{
		"City":     "string",
		"Days":     "integer",
		"Verbose":  "boolean",
		"Untagged": "number",
	}
	for _, arg := range tool.GetParameters() {
		prop, ok := fn.Parameters.Properties[arg.Name]
		if !ok {
			t.Fatalf("Parameters.Properties missing key %q", arg.Name)
		}
		if wantType, ok := wantTypes[arg.Name]; ok && prop.Type != wantType {
			t.Fatalf("Properties[%q].Type = %q, want %q", arg.Name, prop.Type, wantType)
		}
		if prop.Description != arg.Description {
			t.Fatalf("Properties[%q].Description = %q, want %q", arg.Name, prop.Description, arg.Description)
		}
	}
}
