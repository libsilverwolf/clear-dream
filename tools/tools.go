package tools

import (
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/libsilverwolf/clear-dream/model"
	"github.com/sirupsen/logrus"
)

type ToolArgument struct {
	Name        string
	Type        string
	Description string
	// goType is the reflected field type populated by ExtractToolParams. It is
	// used to build an accurate JSON schema for named structs, slices, maps and
	// pointers, where Type's string form does not reveal the underlying kind.
	// Arguments constructed by hand may leave it nil; Type is parsed instead.
	goType reflect.Type
}

type ToolFunction func(input any, args any) (any, error)

type Tool interface {
	Description() string
	GetParameters() []ToolArgument
	// Run executes the tool and returns the result.
	Run(params any, context any) (any, error)
}

type ToolRegistry struct {
	logger *logrus.Entry
	// tools is a map of tool names to their corresponding Tool implementations.
	tools *sync.Map
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		logger: logrus.WithField("module", "tool-registry"),
		tools:  &sync.Map{},
	}
}

func (tr *ToolRegistry) RegisterTool(name string, tool Tool) {
	if _, exists := tr.tools.Load(name); exists {
		tr.logger.Warnf("Tool with name '%s' is already registered. Overwriting.", name)
	}
	tr.tools.Store(name, tool)
	tr.logger.Infof("Registered tool: %s", name)
}

// GetTool returns the tool implementation by name; ok is false when not registered.
func (r *ToolRegistry) GetTool(name string) (Tool, bool) {

	tool, ok := r.tools.Load(name)
	if !ok {
		return nil, false
	}

	return tool.(Tool), true
}

// HasTool reports whether the tool is registered.
func (r *ToolRegistry) HasTool(name string) bool {
	_, ok := r.GetTool(name)
	return ok
}

// GetToolDefinition returns the definition of the named tool; ok is false when not registered.
func (r *ToolRegistry) GetToolDefinition(name string) (model.Tool, bool) {
	tool, ok := r.GetTool(name)
	if !ok {
		return model.Tool{}, false
	}

	return BuildToolDefinition(name, tool), true
}

// GetToolDefinitions returns the definitions of all tools in the same order as Names.
func (r *ToolRegistry) GetToolDefinitions() []model.Tool {

	names := []string{}
	r.tools.Range(func(key, value any) bool {
		names = append(names, key.(string))
		return true
	})
	sort.Strings(names)

	definitions := make([]model.Tool, 0, len(names))
	for _, name := range names {
		tool, ok := r.tools.Load(name)
		if !ok {
			r.logger.Warnf("Tool with name '%s' was registered but could not be loaded.", name)
			continue
		}

		definitions = append(definitions, BuildToolDefinition(name, tool.(Tool)))
	}

	return definitions
}

// BuildToolDefinition converts an internal Tool description into a model-ready
// function definition. Each argument's Go type is mapped onto the JSON Schema
// types accepted by the chat completions API.
func BuildToolDefinition(name string, tool Tool) model.Tool {
	if tool == nil {
		return model.Tool{}
	}

	args := tool.GetParameters()
	properties := make(map[string]model.JSONSchema, len(args))
	required := make([]string, 0, len(args))

	for _, arg := range args {
		if arg.Name == "" {
			continue
		}
		properties[arg.Name] = jsonSchemaForArgument(arg)
		required = append(required, arg.Name)
	}

	parameters := model.JSONSchema{
		Type:       "object",
		Properties: properties,
	}
	if len(required) > 0 {
		parameters.Required = required
	}

	return model.Tool{
		Type: "function",
		Function: model.FunctionSchema{
			Name:        name,
			Description: tool.Description(),
			Parameters:  parameters,
		},
	}
}

// jsonSchemaForArgument derives the JSON schema of a single tool argument,
// preferring the reflected Go type extracted from the parameter struct.
func jsonSchemaForArgument(arg ToolArgument) model.JSONSchema {
	schema := jsonSchemaForReflectType(arg.goType)
	if arg.goType == nil {
		schema = jsonSchemaForTypeName(arg.Type)
	}
	schema.Description = arg.Description

	return schema
}

// jsonSchemaForReflectType maps a Go type onto the JSON Schema type an OpenAI
// compatible API understands. Pointers are unwrapped and the element schema is
// used for arrays; unknown kinds fall back to "string".
func jsonSchemaForReflectType(t reflect.Type) model.JSONSchema {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return model.JSONSchema{Type: "string"}
	}

	switch t.Kind() {
	case reflect.Bool:
		return model.JSONSchema{Type: "boolean"}
	case reflect.String:
		return model.JSONSchema{Type: "string"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return model.JSONSchema{Type: "integer"}
	case reflect.Float32, reflect.Float64:
		return model.JSONSchema{Type: "number"}
	case reflect.Slice, reflect.Array:
		items := jsonSchemaForReflectType(t.Elem())
		return model.JSONSchema{Type: "array", Items: &items}
	case reflect.Map:
		additional := true
		return model.JSONSchema{Type: "object", AdditionalProperties: &additional}
	case reflect.Struct:
		// Timestamps are serialized as strings, not as objects.
		if t == reflect.TypeFor[time.Time]() {
			return model.JSONSchema{Type: "string"}
		}
		return model.JSONSchema{Type: "object"}
	default:
		return model.JSONSchema{Type: "string"}
	}
}

// jsonSchemaForTypeName maps a Go type name (as produced by reflect.Type.String)
// or an already-normalized JSON Schema type onto a schema. It is the fallback for
// ToolArguments that were not built by ExtractToolParams.
func jsonSchemaForTypeName(name string) model.JSONSchema {
	name = strings.TrimSpace(name)
	for strings.HasPrefix(name, "*") {
		name = strings.TrimPrefix(name, "*")
	}

	switch name {
	case "", "string":
		return model.JSONSchema{Type: "string"}
	case "bool", "boolean":
		return model.JSONSchema{Type: "boolean"}
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "byte", "rune", "integer":
		return model.JSONSchema{Type: "integer"}
	case "float32", "float64", "number":
		return model.JSONSchema{Type: "number"}
	case "object":
		return model.JSONSchema{Type: "object"}
	case "array":
		return model.JSONSchema{Type: "array"}
	}

	if elem, ok := strings.CutPrefix(name, "[]"); ok {
		items := jsonSchemaForTypeName(elem)
		return model.JSONSchema{Type: "array", Items: &items}
	}
	if rest, ok := strings.CutPrefix(name, "["); ok {
		// Fixed-size arrays look like "[3]int".
		if _, elem, found := strings.Cut(rest, "]"); found {
			items := jsonSchemaForTypeName(elem)
			return model.JSONSchema{Type: "array", Items: &items}
		}
	}
	if strings.HasPrefix(name, "map[") {
		additional := true
		return model.JSONSchema{Type: "object", AdditionalProperties: &additional}
	}

	// Named structs cannot be resolved from a string; assume string as before.
	return model.JSONSchema{Type: "string"}
}
