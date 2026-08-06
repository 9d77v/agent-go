// Package agent provides the ADK-Go wrapper for ADK agents.
// 通用框架层：封装 ADK Runner。审核基于 ADK 原生 HITL，适用于桌面端与服务端场景。
package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"
)

// DefaultUseChatAPI 控制是否默认使用 Chat Completions API 而非 Responses API。
// 设为 true 时，SetModel 会自动切换到 ChatModel 适配器。
var DefaultUseChatAPI = true

// AgentConfig Agent 配置。
type AgentConfig struct {
	// AppName 应用标识（框架层不感知具体含义，调用方自行编码 workspace_id）。
	AppName string

	// SessionService 会话持久化服务。
	SessionService session.Service

	// ArtifactService 制品存储服务（可选）。配置后图片等输入 blob 自动存为 artifact，供 load_artifacts 工具加载。
	ArtifactService artifact.Service

	// MemoryService 记忆服务（可选）。
	MemoryService any

	// MaxIterations 单次编排最大迭代次数。
	MaxIterations int

	// ApprovalTimeout 保留字段（不再使用，兼容配置）。
	ApprovalTimeout time.Duration
}

// Agent 通用 Agent 编排外观。
// 封装 ADK-Go Runner。审核基于 ADK 原生 HITL（工具层 ctx.RequestConfirmation/ToolConfirmation），
// 框架层不再注册 BeforeToolCallback 门禁。
type Agent struct {
	config       AgentConfig
	adkRunner    *AdkRunner
	currentLLM   model.LLM
	tools        []tool.Tool
	instruction  string
	thinkingMode string // 思考模式：off / default / deep

	// 问卷 HITL（askQuestions 工具）：与审批不同，走 channel 等待前端回答
	pendingQuestionnaires map[string]chan string
	approvalMu            sync.Mutex
}

// NewAgent 创建 Agent。
func NewAgent(cfg AgentConfig) *Agent {
	return &Agent{
		config:                cfg,
		pendingQuestionnaires: make(map[string]chan string),
	}
}

// SetModel 设置当前使用的 LLM 模型（使用标准 OpenAI Chat Completions API）。
// 兼容 DeepSeek、Ollama、vLLM 等所有 /v1/chat/completions 兼容的提供商。
func (a *Agent) SetModel(_ context.Context, apiKey, baseURL, modelName string) error {
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
	a.currentLLM = rm
	return nil
}

// SetTools 设置 Agent 可用的工具列表。
func (a *Agent) SetTools(tools []tool.Tool) {
	a.tools = tools
}

// SetInstruction 设置 Agent 的 system instruction。
func (a *Agent) SetInstruction(instruction string) {
	a.instruction = instruction
}

// SetThinking 设置思考模式。会在下一次 BuildAndRun 时生效。
func (a *Agent) SetThinking(mode string) {
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
	if a.currentLLM == nil {
		return fmt.Errorf("model not set, call SetModel first")
	}

	// 创建 LLM Agent（无 BeforeToolCallback 门禁；审核在工具层原生 HITL）
	agt, err := llmagent.New(llmagent.Config{
		Name:        "main",
		Description: "主 Agent",
		Model:       a.currentLLM,
		Instruction: a.instruction,
		Tools:       a.tools,
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

	a.adkRunner = runner
	return runner.runContent(ctx, userID, sessionID, content, callbacks)
}

// ResolveQuestionnaire 处理问卷结果（askQuestions 工具的 channel 回传）。
func (a *Agent) ResolveQuestionnaire(questionnaireID string, answer string) {
	a.approvalMu.Lock()
	ch, ok := a.pendingQuestionnaires[questionnaireID]
	if ok {
		delete(a.pendingQuestionnaires, questionnaireID)
	}
	a.approvalMu.Unlock()

	if ok {
		select {
		case ch <- answer:
		default:
		}
	}
}
