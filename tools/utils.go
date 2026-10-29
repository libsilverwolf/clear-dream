package tools

import (
	"reflect"

	"github.com/rotisserie/eris"
)

type genericTool struct {
	name        string
	description string
	handler     func(any, any) (any, error)
	params      []ToolArgument
}

// Description implements [Tool].
func (g *genericTool) Description() string {
	return g.description
}

// GetParameters implements [Tool].
func (g *genericTool) GetParameters() []ToolArgument {
	return g.params
}

// Run implements [Tool].
func (g *genericTool) Run(params any, context any) (any, error) {
	return g.handler(params, context)
}

var _ Tool = (*genericTool)(nil)

type CreateToolParams struct {
	Name        string
	Description string
	Handler     func(any, any) (any, error)
	ParamStruct any
}

func CreateTool(params CreateToolParams) (Tool, error) {
	if params.Name == "" {
		return nil, eris.New("tool name cannot be empty")
	}
	if params.Description == "" {
		return nil, eris.New("tool description cannot be empty")
	}
	if params.Handler == nil {
		return nil, eris.New("tool handler cannot be nil")
	}

	// Extract parameters from the provided struct type.
	toolArgs, err := ExtractToolParams(params.ParamStruct)
	if err != nil {
		return nil, eris.Wrap(err, "failed to extract tool parameters")
	}
	tool := &genericTool{
		name:        params.Name,
		description: params.Description,
		handler:     params.Handler,
		params:      toolArgs,
	}

	return tool, nil
}

func ExtractToolParams(param any) ([]ToolArgument, error) {
	// A nil param struct means the tool takes no arguments.
	if param == nil {
		return nil, nil
	}

	t := reflect.TypeOf(param)
	if t.Kind() != reflect.Struct {
		return nil, eris.Errorf("expected struct type, got %s", t.Kind())
	}

	var args []ToolArgument
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		arg := ToolArgument{
			Name:        field.Name,
			Type:        field.Type.String(),
			Description: field.Tag.Get("description"),
			goType:      field.Type,
		}
		args = append(args, arg)
	}

	return args, nil
}
