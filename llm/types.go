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
}
type ModelInfo struct {
	ID        string
	CreatedAt int64
	OwnedBy   string
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
