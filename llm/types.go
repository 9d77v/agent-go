// Package llm provides shared LLM data types for agent-go consumers.
package llm

type ChatRequest struct {
	ProviderID, Model string
	Messages          []ChatMessage
	Temperature       float64
	Stream            bool
	Thinking          string
	Tools             bool
	ContextWindow     int

	// 网络/协议参数（由调用方填充，LLMService 据此发起请求）
	ProviderType    string // "openai" / "anthropic" / "gemini"
	BaseURL         string // 完整 base url（如 https://api.deepseek.com）
	APIKey          string
	UseResponsesAPI bool                   // true → /responses，false → /chat/completions
	ToolDefs        []OpenAIToolDefinition // 流式请求的工具定义（可选）
}
type ModelInfo struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"created"`
	OwnedBy   string `json:"owned_by"`
}
type ToolCallInfo struct {
	ID, Type string
	Function struct{ Name, Arguments string }
}
type ChatMessage struct {
	Role, Content, ReasoningContent, ToolCallID string
	ToolCalls                                   []ToolCallInfo
}
type ChatResponse struct {
	Content, ReasoningContent, Model string
	Usage                            *Usage
}
type Usage struct{ PromptTokens, CompletionTokens, TotalTokens int }
type StreamEvent string

const (
	StreamEventContent   StreamEvent = "content"
	StreamEventReasoning StreamEvent = "reasoning"
	StreamEventToolCall  StreamEvent = "tool_call"
	StreamEventDone      StreamEvent = "done"
	StreamEventError     StreamEvent = "error"
)

// ToolCall represents a tool call with name and arguments.
type ToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
