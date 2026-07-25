package llm

// ChatRequest LLM 聊天请求
type ChatRequest struct {
	ProviderID    string        `json:"provider_id"`
	Model         string        `json:"model"`
	Messages      []ChatMessage `json:"messages"`
	Temperature   float64       `json:"temperature"`
	Stream        bool          `json:"stream"`
	Thinking      string        `json:"thinking,omitempty"`
	Tools         bool          `json:"tools,omitempty"`
	ContextWindow int           `json:"context_window,omitempty"`
}

// ModelInfo OpenAI 兼容 API 返回的模型信息
type ModelInfo struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"created"`
	OwnedBy   string `json:"owned_by"`
}

// ToolCall 工具调用信息
type ToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolCallInfo 工具调用信息（包含 ID）
type ToolCallInfo struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ChatMessage 聊天消息
type ChatMessage struct {
	Role             string         `json:"role"`
	Content          string         `json:"content"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	ToolCalls        []ToolCallInfo `json:"tool_calls,omitempty"`
}

// ChatResponse LLM 聊天响应
type ChatResponse struct {
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
	Model            string `json:"model"`
	Usage            *Usage `json:"usage,omitempty"`
}

// Usage token 用量
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamEvent 流式事件类型
type StreamEvent string

const (
	StreamEventContent   StreamEvent = "content"
	StreamEventReasoning StreamEvent = "reasoning"
	StreamEventToolCall  StreamEvent = "tool_call"
	StreamEventDone      StreamEvent = "done"
	StreamEventError     StreamEvent = "error"
)
