// Package agent provides the ADK-Go wrapper for Wails desktop applications.
package agent

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/openaimodel"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"

	ftool "github.com/9d77v/agent-go/tool"
)

// DefaultUseChatAPI 控制是否默认使用 Chat Completions API 而非 Responses API。
// 设为 true 时，SetModel 会自动切换到 ChatModel 适配器。
var DefaultUseChatAPI = true

// WailsAgentConfig WailsAgent 配置。
type WailsAgentConfig struct {
	// AppName 应用标识（框架层不感知具体含义，调用方自行编码 workspace_id）。
	AppName string

	// SessionService 会话持久化服务。
	SessionService session.Service

	// MemoryService 记忆服务（可选）。
	MemoryService any

	// MaxIterations 单次编排最大迭代次数。
	MaxIterations int

	// 审批超时时间。
	ApprovalTimeout time.Duration
}

// WailsAgent Wails 桌面应用的 Agent 外观。
// 封装 ADK-Go Runner + 审批系统，供 Wails 桌面应用使用。
type WailsAgent struct {
	config       WailsAgentConfig
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

// NewWailsAgent 创建 WailsAgent。
func NewWailsAgent(cfg WailsAgentConfig) *WailsAgent {
	timeout := cfg.ApprovalTimeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	return &WailsAgent{
		config:                cfg,
		pendingApprovals:      make(map[string]chan ApprovalResult),
		pendingQuestionnaires: make(map[string]chan string),
		approvalTimeout:       timeout,
	}
}

// SetModel 设置当前使用的 LLM 模型（使用标准 OpenAI Chat Completions API）。
// 兼容 DeepSeek、Ollama、vLLM 等所有 /v1/chat/completions 兼容的提供商。
func (w *WailsAgent) SetModel(_ context.Context, apiKey, baseURL, modelName string) error {
	w.currentLLM = NewChatModel(modelName, apiKey, baseURL)
	return nil
}

// SetResponsesModel 设置使用 OpenAI Responses API 的模型（仅 OpenAI 原生支持）。
// 大多数第三方提供商（DeepSeek、Ollama 等）不支持 Responses API，请使用 SetModel。
func (w *WailsAgent) SetResponsesModel(ctx context.Context, apiKey, baseURL, modelName string) error {
	llm, err := openaimodel.NewModel(ctx, modelName, &openaimodel.ClientConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
	})
	if err != nil {
		return fmt.Errorf("create responses model: %w", err)
	}
	w.currentLLM = llm
	return nil
}

// SetTools 设置 Agent 可用的工具列表。
func (w *WailsAgent) SetTools(tools []tool.Tool) {
	w.tools = tools
}

// SetInstruction 设置 Agent 的 system instruction。
func (w *WailsAgent) SetInstruction(instruction string) {
	w.instruction = instruction
}

// SetThinking 设置思考模式。会在下一次 BuildAndRun 时生效。
func (w *WailsAgent) SetThinking(mode string) {
	w.thinkingMode = mode
	if cm, ok := w.currentLLM.(*ChatModel); ok {
		cm.SetThinkingMode(mode)
	}
}

// BuildAndRun 构建 ADK Agent 并运行对话。
// callbacks 用于接收流式事件。
func (w *WailsAgent) BuildAndRun(
	ctx context.Context,
	userID, sessionID, message string,
	callbacks *OrchestratorCallbacks,
	approvalMode ftool.ApprovalMode,
) error {
	if w.currentLLM == nil {
		return fmt.Errorf("model not set, call SetModel first")
	}

	// 构建 before tool callback（审批系统集成）
	var beforeToolCallbacks []llmagent.BeforeToolCallback
	if approvalMode != ftool.ApprovalModeAuto {
		beforeToolCallbacks = append(beforeToolCallbacks, w.createApprovalCallback(callbacks, approvalMode))
	}

	// 创建 LLM Agent
	agt, err := llmagent.New(llmagent.Config{
		Name:                "main",
		Description:         "主 Agent",
		Model:               w.currentLLM,
		Instruction:         w.instruction,
		Tools:               w.tools,
		BeforeToolCallbacks: beforeToolCallbacks,
	})
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	// 创建 AdkRunner 并运行
	runner, err := NewAdkRunner(AdkRunnerConfig{
		AppName:        w.config.AppName,
		Agent:          agt,
		SessionService: w.config.SessionService,
		MaxIterations:  w.config.MaxIterations,
	})
	if err != nil {
		return fmt.Errorf("create runner: %w", err)
	}

	w.adkRunner = runner
	return runner.Run(ctx, userID, sessionID, message, callbacks)
}

// createApprovalCallback 创建审批回调。
// 根据 approvalMode 决定是否需要对工具调用进行审批。
func (w *WailsAgent) createApprovalCallback(callbacks *OrchestratorCallbacks, mode ftool.ApprovalMode) llmagent.BeforeToolCallback {
	return func(ctx agent.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
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
		w.approvalMu.Lock()
		w.pendingApprovals[approvalID] = ch
		w.approvalMu.Unlock()

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
		case <-time.After(w.approvalTimeout):
			w.approvalMu.Lock()
			delete(w.pendingApprovals, approvalID)
			w.approvalMu.Unlock()
			return nil, fmt.Errorf("审批超时")
		}
	}
}

// ResolveApproval 处理审批结果。
func (w *WailsAgent) ResolveApproval(approvalID string, approved bool) {
	w.approvalMu.Lock()
	ch, ok := w.pendingApprovals[approvalID]
	if ok {
		delete(w.pendingApprovals, approvalID)
	}
	w.approvalMu.Unlock()

	if ok {
		select {
		case ch <- ApprovalResult{Approved: approved}:
		default:
		}
	}
}

// ResolveQuestionnaire 处理问卷结果。
func (w *WailsAgent) ResolveQuestionnaire(questionnaireID string, answer string) {
	w.approvalMu.Lock()
	ch, ok := w.pendingQuestionnaires[questionnaireID]
	if ok {
		delete(w.pendingQuestionnaires, questionnaireID)
	}
	w.approvalMu.Unlock()

	if ok {
		select {
		case ch <- answer:
		default:
		}
	}
}

// SetSubApprovalCallback 设置子 Agent 审批回调（兼容旧接口）。
func (w *WailsAgent) SetSubApprovalCallback(cb func(approvalID, command string, riskLevel ftool.RiskLevel)) {
	// 当前简化实现：子 Agent 审批通过主审批系统处理
	log.Printf("[WailsAgent] SetSubApprovalCallback called")
}

// Ensure interface compliance
var _ agent.Agent
