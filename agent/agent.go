// Package agent provides the ADK-Go wrapper for ADK agents.
// 通用框架层：封装 ADK Runner + 审批系统，适用于桌面端与服务端场景。
package agent

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"

	ftool "github.com/9d77v/agent-go/tool"
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

	// 审批超时时间。
	ApprovalTimeout time.Duration
}

// Agent 通用 Agent 编排外观。
// 封装 ADK-Go Runner + 审批系统，适用于桌面端与服务端场景。
type Agent struct {
	config       AgentConfig
	adkRunner    *AdkRunner
	currentLLM   model.LLM
	tools        []tool.Tool
	instruction  string
	thinkingMode string // 思考模式：off / default / deep

	// 审批系统
	pendingApprovals      map[string]chan ApprovalResult
	approvalMu            sync.Mutex
	pendingQuestionnaires map[string]chan string
	approvalTimeout       time.Duration
}

// NewAgent 创建 Agent。
func NewAgent(cfg AgentConfig) *Agent {
	timeout := cfg.ApprovalTimeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	return &Agent{
		config:                cfg,
		pendingApprovals:      make(map[string]chan ApprovalResult),
		pendingQuestionnaires: make(map[string]chan string),
		approvalTimeout:       timeout,
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

// BuildAndRun 构建 ADK Agent 并运行对话。
// callbacks 用于接收流式事件。
func (a *Agent) BuildAndRun(
	ctx context.Context,
	userID, sessionID, message string,
	callbacks *OrchestratorCallbacks,
	approvalMode ftool.ApprovalMode,
) error {
	if a.currentLLM == nil {
		return fmt.Errorf("model not set, call SetModel first")
	}

	// 构建 before tool callback（审批系统集成）
	var beforeToolCallbacks []llmagent.BeforeToolCallback
	if approvalMode != ftool.ApprovalModeAuto {
		beforeToolCallbacks = append(beforeToolCallbacks, a.createApprovalCallback(callbacks, approvalMode))
	}

	// 创建 LLM Agent
	agt, err := llmagent.New(llmagent.Config{
		Name:                "main",
		Description:         "主 Agent",
		Model:               a.currentLLM,
		Instruction:         a.instruction,
		Tools:               a.tools,
		BeforeToolCallbacks: beforeToolCallbacks,
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
	return runner.Run(ctx, userID, sessionID, message, callbacks)
}

// createApprovalCallback 创建审批回调。
// 根据 approvalMode 决定是否需要对工具调用进行审批。
func (a *Agent) createApprovalCallback(callbacks *OrchestratorCallbacks, mode ftool.ApprovalMode) llmagent.BeforeToolCallback {
	return func(ctx adkagent.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
		// 获取工具的风险级别
		risk := ftool.RiskSafe
		if mode == ftool.ApprovalModeManual {
			risk = ftool.RiskDangerous
		}

		// 仅对危险操作需要审批
		if risk == ftool.RiskSafe {
			return nil, nil // 不需要审批，继续执行
		}

		// 发送审批请求
		approvalID := fmt.Sprintf("approval_%s_%d", t.Name(), time.Now().UnixNano())
		command := fmt.Sprintf("%s: %v", t.Name(), args)

		ch := make(chan ApprovalResult, 1)
		a.approvalMu.Lock()
		a.pendingApprovals[approvalID] = ch
		a.approvalMu.Unlock()

		if callbacks.OnApprovalRequired != nil {
			callbacks.OnApprovalRequired(approvalID, "", command, risk)
		}

		// 阻塞等待审批结果
		select {
		case result := <-ch:
			if !result.Approved {
				return nil, fmt.Errorf("用户拒绝了此操作")
			}
			return nil, nil // 已批准，继续执行
		case <-time.After(a.approvalTimeout):
			a.approvalMu.Lock()
			delete(a.pendingApprovals, approvalID)
			a.approvalMu.Unlock()
			return nil, fmt.Errorf("审批超时")
		}
	}
}

// ResolveApproval 处理审批结果。
func (a *Agent) ResolveApproval(approvalID string, approved bool) {
	a.approvalMu.Lock()
	ch, ok := a.pendingApprovals[approvalID]
	if ok {
		delete(a.pendingApprovals, approvalID)
	}
	a.approvalMu.Unlock()

	if ok {
		select {
		case ch <- ApprovalResult{Approved: approved}:
		default:
		}
	}
}

// ResolveQuestionnaire 处理问卷结果。
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

// SetSubApprovalCallback 设置子 Agent 审批回调（兼容旧接口）。
func (a *Agent) SetSubApprovalCallback(cb func(approvalID, command string, riskLevel ftool.RiskLevel)) {
	// 当前简化实现：子 Agent 审批通过主审批系统处理
	log.Printf("[Agent] SetSubApprovalCallback called")
}
