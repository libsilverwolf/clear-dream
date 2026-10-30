package agent

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/libsilverwolf/clear-dream/model"
	"github.com/libsilverwolf/clear-dream/tools"
	"github.com/rotisserie/eris"
	"github.com/sirupsen/logrus"
)

const (
	// defaultModel is used when OPENAI_MODEL is not configured.
	defaultModel = "deepseek-v4.1-flash"
	// maxToolRounds bounds how many completion round-trips may request tool calls.
	maxToolRounds = 16
	// requestTimeout bounds a single completion request so a stuck API cannot
	// block the bot forever.
	requestTimeout = 120 * time.Second
)

type toolRunResult struct {
	Success bool
	Error   string
	Content string
}

// encodeToolResult renders a tool result for the model. Plain strings are passed
// through unchanged; any other value is JSON-encoded.
func encodeToolResult(result any) (string, error) {
	if text, ok := result.(string); ok {
		return text, nil
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return "", eris.Wrap(err, "failed to encode tool result")
	}

	return string(encoded), nil
}

type Model struct {
	logger        *logrus.Entry
	toolsRegistry *tools.ToolRegistry
	apiClient     OpenAiCompatableClient
	defaultModel  string
}

type NewModelParams struct {
	ToolsRegistry *tools.ToolRegistry
	ApiClient     OpenAiCompatableClient
	// DefaultModel overrides defaultModel when non-empty.
	DefaultModel string
}

func NewModel(params *NewModelParams) *Model {
	modelName := params.DefaultModel
	if modelName == "" {
		modelName = defaultModel
	}

	return &Model{
		logger:        logrus.WithField("module", "agent"),
		toolsRegistry: params.ToolsRegistry,
		defaultModel:  modelName,
		apiClient:     params.ApiClient,
	}
}

// runToolCall executes a single tool call and renders the outbound tool message.
func (a *Model) runToolCall(toolCall model.ToolCall, ctx model.LlmContext) (model.Message, error) {
	tool, ok := a.toolsRegistry.GetTool(toolCall.Function.Name)
	if !ok {
		return model.Message{}, eris.Errorf("tool %q not found", toolCall.Function.Name)
	}

	result, runErr := tool.Run(toolCall.Function.Arguments, ctx)
	// report the tool outcome back to the model
	outcome := toolRunResult{Success: runErr == nil}
	if runErr != nil {
		a.logger.WithFields(logrus.Fields{
			"tool_call_id":     toolCall.ID,
			"tool_name":        toolCall.Function.Name,
			"tool_call_params": fmt.Sprintf("%+v", toolCall.Function.Arguments),
			"run_error":        runErr,
		}).Error("tool call return error")
		outcome.Error = runErr.Error()
	}
	if result != nil {
		content, err := encodeToolResult(result)
		if err != nil {
			outcome.Success = false
			outcome.Error = err.Error()
		} else {
			outcome.Content = content
		}
	}

	contentText, err := json.Marshal(outcome)
	if err != nil {
		return model.Message{}, eris.Wrap(err, "failed to encode tool result")
	}

	return model.Message{
		Role:       model.RoleTool,
		ToolCallID: toolCall.ID,
		Content:    string(contentText),
	}, nil
}

// Invoke runs a tool-calling loop until the model answers without tool calls.
func (a *Model) Invoke(input string) (string, error) {
	request := model.ChatCompletionRequest{
		Model: a.defaultModel,
		Messages: []model.Message{
			// model.NewTextMessage(model.RoleSystem, SYSTEM_PROMPT),
			model.NewTextMessage(model.RoleUser, input),
		},
		Tools: a.toolsRegistry.GetToolDefinitions(),
	}

	for round := 0; ; round++ {
		if round >= maxToolRounds {
			return "", eris.Errorf("exceeded the limit of %d tool rounds", maxToolRounds)
		}

		response, err := a.apiClient.GetResponse(request)
		if err != nil {
			return "", err
		}
		if len(response.Choices) == 0 {
			return "", eris.New("completion response contains no choices")
		}

		choice := response.Choices[0]
		if choice.FinishReason == "stop" {
			return choice.Message.Content, nil
		}
		if len(choice.Message.ToolCalls) == 0 {
			return "", eris.Errorf("completion finished with reason %q and no tool calls", choice.FinishReason)
		}

		// fill back the assistant message that requested the tool calls
		assistantMessage := choice.Message
		if assistantMessage.Role == "" {
			assistantMessage.Role = model.RoleAssistant
		}
		request.Messages = append(request.Messages, assistantMessage)

		// call tools and reply with their results
		for _, toolCall := range choice.Message.ToolCalls {
			a.logger.WithFields(logrus.Fields{
				"tool_call_id":     toolCall.ID,
				"tool_name":        toolCall.Function.Name,
				"round":            round,
				"tool_call_params": fmt.Sprintf("%+v", toolCall.Function.Arguments),
			}).Info("executing tool call")
			toolMessage, err := a.runToolCall(toolCall, nil)
			if err != nil {
				return "", err
			}
			request.Messages = append(request.Messages, toolMessage)
		}
	}
}
