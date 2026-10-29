package model

import "encoding/json"

// ==========================================
// 1. Chat Completion Request
// ==========================================

// ChatCompletionRequest represents the payload for POST /v1/chat/completions.
type ChatCompletionRequest struct {
	Model               string          `json:"model"`
	Messages            []Message       `json:"messages"`
	Tools               []Tool          `json:"tools,omitempty"`
	ToolChoice          any             `json:"tool_choice,omitempty"`         // Allowed: "none", "auto", "required", or NamedToolChoice
	ParallelToolCalls   *bool           `json:"parallel_tool_calls,omitempty"` // Controls whether to allow parallel tool calls
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	N                   *int            `json:"n,omitempty"`
	Stream              bool            `json:"stream,omitempty"`
	Stop                any             `json:"stop,omitempty"` // string or []string
	MaxTokens           *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int            `json:"max_completion_tokens,omitempty"`
	PresencePenalty     *float64        `json:"presence_penalty,omitempty"`
	FrequencyPenalty    *float64        `json:"frequency_penalty,omitempty"`
	ResponseFormat      *ResponseFormat `json:"response_format,omitempty"` // "text", "json_object", or "json_schema"
	Seed                *int64          `json:"seed,omitempty"`
	User                string          `json:"user,omitempty"`
}

// ==========================================
// 2. Messages & Multimodal Payloads
// ==========================================

// Role represents the role of a message author.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message represents a single chat completion message.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`                // string or []ContentPart (multimodal)
	Name       string     `json:"name,omitempty"`         // Optional name for system/user/assistant
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // Present on assistant messages invoking tools
	ToolCallID string     `json:"tool_call_id,omitempty"` // Required when Role == "tool"
}

// ContentPartType denotes the type of multimodal content part.
type ContentPartType string

const (
	ContentPartTypeText     ContentPartType = "text"
	ContentPartTypeImageURL ContentPartType = "image_url"
)

// ContentPart represents an individual part of a multimodal content array.
type ContentPart struct {
	Type     ContentPartType `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *ImageURL       `json:"image_url,omitempty"`
}

// ImageURL describes an image payload, either a remote URL or a base64 Data URL.
type ImageURL struct {
	URL    string `json:"url"`              // HTTP/HTTPS URL or "data:image/jpeg;base64,..."
	Detail string `json:"detail,omitempty"` // "auto", "low", "high"
}

// ==========================================
// 3. Tools & Function Schemas
// ==========================================

// Tool wraps a specific tool specification.
type Tool struct {
	Type     string         `json:"type"` // Currently always "function"
	Function FunctionSchema `json:"function"`
}

// FunctionSchema describes metadata and the JSON Schema parameters of a function.
type FunctionSchema struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Strict      *bool      `json:"strict,omitempty"` // Enforces Structured Outputs validation
	Parameters  JSONSchema `json:"parameters"`       // Must have root type "object"
}

// JSONSchema defines a standard JSON Schema draft for tool parameters and structured outputs.
type JSONSchema struct {
	Type                 string                `json:"type"` // "object", "string", "number", "integer", "boolean", "array"
	Description          string                `json:"description,omitempty"`
	Properties           map[string]JSONSchema `json:"properties,omitempty"`
	Items                *JSONSchema           `json:"items,omitempty"` // Required when type == "array"
	Required             []string              `json:"required,omitempty"`
	Enum                 []any                 `json:"enum,omitempty"`
	AdditionalProperties *bool                 `json:"additionalProperties,omitempty"` // Set to false when strict == true
}

// ToolCall represents a function invocation requested by the model.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // Always "function"
	Function FunctionCallData `json:"function"`
}

// FunctionCallData contains the target function name and serialized argument string.
type FunctionCallData struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // Serialized JSON string
}

// NamedToolChoice forces the model to call a specific tool.
type NamedToolChoice struct {
	Type     string              `json:"type"` // Always "function"
	Function NamedFunctionChoice `json:"function"`
}

// NamedFunctionChoice identifies the specific function to invoke.
type NamedFunctionChoice struct {
	Name string `json:"name"`
}

// ==========================================
// 4. Response Format & Structured Outputs
// ==========================================

// ResponseFormatType represents the supported output schema formats.
type ResponseFormatType string

const (
	ResponseFormatText       ResponseFormatType = "text"
	ResponseFormatJSONObject ResponseFormatType = "json_object"
	ResponseFormatJSONSchema ResponseFormatType = "json_schema"
)

// ResponseFormat controls the formatting of model outputs.
type ResponseFormat struct {
	Type       ResponseFormatType  `json:"type"`
	JSONSchema *ResponseJSONSchema `json:"json_schema,omitempty"`
}

// ResponseFormatJSONSchema specifies the schema configuration for Structured Outputs.
type ResponseJSONSchema struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Schema      JSONSchema `json:"schema"`
	Strict      *bool      `json:"strict,omitempty"`
}

// ==========================================
// 5. Chat Completion Response
// ==========================================

// ChatCompletionResponse represents the full payload returned by a non-streaming completion.
type ChatCompletionResponse struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
	Choices           []Choice `json:"choices"`
	Usage             Usage    `json:"usage"`
}

// Choice represents a completion choice returned by the model.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"` // "stop", "tool_calls", "length", "content_filter"
}

// Usage captures token accounting metrics for the request.
type Usage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

// PromptTokensDetails provides a breakdown of prompt tokens, including cache hits.
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
	AudioTokens  int `json:"audio_tokens,omitempty"`
}

// CompletionTokensDetails provides details regarding generated tokens, including reasoning effort.
type CompletionTokensDetails struct {
	ReasoningTokens          int `json:"reasoning_tokens"`
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens,omitempty"`
	RejectedPredictionTokens int `json:"rejected_prediction_tokens,omitempty"`
}

// ==========================================
// 6. Streaming Chunks (Server-Sent Events)
// ==========================================

// ChatCompletionChunk represents an individual streaming event payload.
type ChatCompletionChunk struct {
	ID                string        `json:"id"`
	Object            string        `json:"object"`
	Created           int64         `json:"created"`
	Model             string        `json:"model"`
	SystemFingerprint string        `json:"system_fingerprint,omitempty"`
	Choices           []ChunkChoice `json:"choices"`
	Usage             *Usage        `json:"usage,omitempty"` // Emitted when stream_options.include_usage is enabled
}

// ChunkChoice represents an incremental choice update in a streaming chunk.
type ChunkChoice struct {
	Index        int     `json:"index"`
	Delta        Delta   `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

// Delta contains newly arrived token fragments or tool invocation deltas.
type Delta struct {
	Role      Role            `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	ToolCalls []ChunkToolCall `json:"tool_calls,omitempty"`
}

// ChunkToolCall describes an incremental tool call fragment in a stream.
type ChunkToolCall struct {
	Index    int                   `json:"index"`
	ID       string                `json:"id,omitempty"`
	Type     string                `json:"type,omitempty"`
	Function ChunkFunctionCallData `json:"function,omitempty"`
}

// ChunkFunctionCallData contains incremental updates to function name and argument fragments.
type ChunkFunctionCallData struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"` // Incremental fragments of the JSON string
}

// ==========================================
// 7. API Error Response
// ==========================================

// APIErrorResponse represents the standard error response body returned by the API.
type APIErrorResponse struct {
	Error APIError `json:"error"`
}

// APIError encapsulates error details emitted by the gateway.
type APIError struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
}

// ==========================================
// 8. Helper Methods
// ==========================================

// NewTextMessage creates a standard text-based message.
func NewTextMessage(role Role, text string) Message {
	return Message{
		Role:    role,
		Content: text,
	}
}

// NewToolResponseMessage builds a role="tool" message to report the result back to the model.
func NewToolResponseMessage(callID string, resultJSON string) Message {
	return Message{
		Role:       RoleTool,
		ToolCallID: callID,
		Content:    resultJSON,
	}
}

// ParseArguments deserializes the function argument JSON string into the target struct.
func (tc *ToolCall) ParseArguments(target any) error {
	return json.Unmarshal([]byte(tc.Function.Arguments), target)
}
