// Package agent provides the ADK-Go wrapper for ADK agents.
// 通用框架层：封装 ADK Runner。审核基于 ADK 原生 HITL，适用于桌面端与服务端场景。
package agent

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"
)

// AgentConfig Agent 配置。
type AgentConfig struct {
	// AppName 应用标识（框架层不感知具体含义，调用方自行编码 workspace_id）。
	AppName string

	// SessionService 会话持久化服务。
	SessionService session.Service

	// ArtifactService 制品存储服务（可选）。配置后图片等输入 blob 自动存为 artifact，供 load_artifacts 工具加载。
	ArtifactService artifact.Service

	// MaxIterations 单次编排最大迭代次数。
	MaxIterations int
}

// Agent 通用 Agent 编排外观。
// 封装 ADK-Go Runner。审核基于 ADK 原生 HITL（工具层 ctx.RequestConfirmation/ToolConfirmation），
// 框架层不再注册 BeforeToolCallback 门禁。
// 可变状态（currentLLM/tools/instruction/thinkingMode）由 mu 保护，
// buildAndRun 在锁内快照后使用，支持并发编排不同会话。
type Agent struct {
	config       AgentConfig
	mu           sync.Mutex
	currentLLM   model.LLM
	tools        []tool.Tool
	instruction  string
	thinkingMode string // 思考模式：off / default / deep
}

// NewAgent 创建 Agent。
func NewAgent(cfg AgentConfig) *Agent {
	return &Agent{config: cfg}
}

// SetModel 设置当前使用的 LLM 模型（使用标准 OpenAI Chat Completions API）。
// 兼容 DeepSeek、Ollama、vLLM 等所有 /v1/chat/completions 兼容的提供商。
func (a *Agent) SetModel(_ context.Context, apiKey, baseURL, modelName string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.currentLLM = NewChatModel(modelName, apiKey, baseURL)
	return nil
}

// SetResponsesModel 设置使用 OpenAI Responses API 的模型（/v1/responses）。
// 支持 OpenAI 原生及已兼容 Responses API 的提供商（如 DeepSeek）。
func (a *Agent) SetResponsesModel(ctx context.Context, apiKey, baseURL, modelName string) error {
	rm, err := NewResponsesModel(ctx, modelName, apiKey, baseURL)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.currentLLM = rm
	return nil
}

// SetTools 设置 Agent 可用的工具列表。
func (a *Agent) SetTools(tools []tool.Tool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tools = tools
}

// SetInstruction 设置 Agent 的 system instruction。
func (a *Agent) SetInstruction(instruction string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.instruction = instruction
}

// SetThinking 设置思考模式。会在下一次 BuildAndRun 时生效。
func (a *Agent) SetThinking(mode string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.thinkingMode = mode
	if cm, ok := a.currentLLM.(*ChatModel); ok {
		cm.SetThinkingMode(mode)
	}
}

// BuildAndRun 构建 ADK Agent 并运行对话（文本输入）。
// 审核不在此注册门禁——判定在工具执行层，通过 ADK 原生 HITL（ctx.RequestConfirmation）实现。
func (a *Agent) BuildAndRun(
	ctx context.Context,
	userID, sessionID, message string,
	callbacks *OrchestratorCallbacks,
) error {
	return a.buildAndRun(ctx, userID, sessionID, genai.NewContentFromText(message, "user"), callbacks)
}

// BuildAndRunWithContent 与 BuildAndRun 相同，但接受任意输入 Content。
// 用于 ADK 原生 HITL 审批恢复：以 FunctionResponse（adk_request_confirmation）作为输入再次 Run 同一 session。
func (a *Agent) BuildAndRunWithContent(
	ctx context.Context,
	userID, sessionID string,
	content *genai.Content,
	callbacks *OrchestratorCallbacks,
) error {
	if content == nil {
		return fmt.Errorf("content is required")
	}
	return a.buildAndRun(ctx, userID, sessionID, content, callbacks)
}

// buildAndRun 构建 ADK Agent 并运行对话。
func (a *Agent) buildAndRun(
	ctx context.Context,
	userID, sessionID string,
	content *genai.Content,
	callbacks *OrchestratorCallbacks,
) error {
	// 快照读取：构建期间并发 SetXxx 不影响本次运行
	a.mu.Lock()
	currentLLM := a.currentLLM
	tools := append([]tool.Tool(nil), a.tools...)
	instruction := a.instruction
	a.mu.Unlock()

	if currentLLM == nil {
		return fmt.Errorf("model not set, call SetModel first")
	}

	// 创建 LLM Agent（无 BeforeToolCallback 门禁；审核在工具层原生 HITL）
	agt, err := llmagent.New(llmagent.Config{
		Name:        "main",
		Description: "主 Agent",
		Model:       currentLLM,
		Instruction: instruction,
		Tools:       tools,
	})
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	// 创建 AdkRunner 并运行
	runner, err := NewAdkRunner(AdkRunnerConfig{
		AppName:         a.config.AppName,
		Agent:           agt,
		SessionService:  a.config.SessionService,
		ArtifactService: a.config.ArtifactService,
		MaxIterations:   a.config.MaxIterations,
	})
	if err != nil {
		return fmt.Errorf("create runner: %w", err)
	}

	return runner.runContent(ctx, userID, sessionID, content, callbacks)
}
