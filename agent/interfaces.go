package agent

import (
	"context"
	"encoding/json"

	"github.com/9d77v/agent-go/llm"
	"github.com/9d77v/agent-go/tool"
)

// ---------- LLM 服务接口 ----------

// LLMService LLM 调用接口
type LLMService interface {
	Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error)
	ChatStream(
		ctx context.Context,
		req *llm.ChatRequest,
		onContent func(delta string),
		onReasoning func(delta string),
		onToolCallStart func(callID, name string),
		onToolCallDelta func(callID, argsDelta string),
		onToolCallEnd func(callID, name, arguments string),
	) (*llm.ChatResponse, error)
}

// ---------- Tool 执行器接口 ----------

// ToolExecutor 工具执行接口（供编排器使用）
type ToolExecutor interface {
	Execute(toolName string, args json.RawMessage) *tool.ToolResult
}

// ---------- System Prompt 构建接口 ----------

// SystemPromptBuilder 构建 system prompt 的接口
type SystemPromptBuilder interface {
	BuildSystemPrompt(mode string, contextInfo string) string
	BuildTitlePrompt() string
}

// ---------- Tool 定义提供接口 ----------

// ToolDefinitionProvider 提供 LLM 工具定义的接口
type ToolDefinitionProvider interface {
	GetToolDefinitions() []tool.ToolDefinition
	IsToolEnabled(name string) bool
}
