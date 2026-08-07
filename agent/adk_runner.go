// Package agent provides the ADK-Go wrapper for ADK agents.
// 通用框架层：封装 ADK Runner，适用于桌面端与服务端场景。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	adktoolconfirmation "google.golang.org/adk/v2/tool/toolconfirmation"
	"google.golang.org/genai"

	ftool "github.com/9d77v/agent-go/tool"
)

// AdkRunnerConfig 配置 ADK Runner 的参数。
type AdkRunnerConfig struct {
	// AppName 应用名称，用于 session/memory/artifact 隔离。
	AppName string

	// Agent ADK Agent 实例（由调用方通过 llmagent.New 等创建）。
	Agent adkagent.Agent

	// SessionService 会话持久化服务。
	SessionService session.Service

	// ArtifactService 制品存储服务（可选）。配置后启用输入 blob（图片等）→ artifact 机制（SaveInputBlobsAsArtifacts）。
	ArtifactService artifact.Service

	// MaxIterations 最大迭代次数限制（0 表示不限制）。
	MaxIterations int

	// StreamingMode 流式模式（默认 StreamingModeSSE，全局强制流式输出）。
	// 作为内部扩展点：未来如需按供应商关闭流式，可在此注入配置。
	StreamingMode adkagent.StreamingMode
}

// AdkRunner 封装 ADK-Go Runner，将 Event 流转换为 OrchestratorCallbacks。
type AdkRunner struct {
	runner *runner.Runner
	config AdkRunnerConfig
}

// NewAdkRunner 创建 AdkRunner。
func NewAdkRunner(cfg AdkRunnerConfig) (*AdkRunner, error) {
	if cfg.AppName == "" {
		return nil, fmt.Errorf("AppName is required")
	}
	if cfg.Agent == nil {
		return nil, fmt.Errorf("Agent is required")
	}
	if cfg.SessionService == nil {
		return nil, fmt.Errorf("SessionService is required")
	}

	// 默认 SSE 流式（全局强制）；空值回退到非流式以保持向后兼容。
	if cfg.StreamingMode == "" {
		cfg.StreamingMode = adkagent.StreamingModeSSE
	}

	r, err := runner.New(runner.Config{
		AppName:           cfg.AppName,
		Agent:             cfg.Agent,
		SessionService:    cfg.SessionService,
		ArtifactService:   cfg.ArtifactService,
		AutoCreateSession: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create runner: %w", err)
	}

	return &AdkRunner{
		runner: r,
		config: cfg,
	}, nil
}

// confirmationPlaceholder 确认等待中的占位结果文本（HITL）。
const confirmationPlaceholder = "requires confirmation"

// Run 运行 Agent 对话（文本输入），将 ADK Event 流转换为 OrchestratorCallbacks 回调。
func (r *AdkRunner) Run(
	ctx context.Context,
	userID, sessionID, message string,
	callbacks *OrchestratorCallbacks,
) error {
	return r.runContent(ctx, userID, sessionID, genai.NewContentFromText(message, "user"), callbacks)
}

// RunWithContent 运行 Agent，支持任意输入 Content。
// 用于 ADK 原生 HITL 审批恢复：客户端批准/拒绝后，以 FunctionResponse（adk_request_confirmation）
// 作为输入再次 Run 同一 session，ADK 的 RequestConfirmationRequestProcessor 会重放原始工具调用。
func (r *AdkRunner) RunWithContent(
	ctx context.Context,
	userID, sessionID string,
	content *genai.Content,
	callbacks *OrchestratorCallbacks,
) error {
	if content == nil {
		return fmt.Errorf("content is required")
	}
	return r.runContent(ctx, userID, sessionID, content, callbacks)
}

// runContent 运行 Agent，将 ADK Event 流转换为 OrchestratorCallbacks 回调。
func (r *AdkRunner) runContent(
	ctx context.Context,
	userID, sessionID string,
	content *genai.Content,
	callbacks *OrchestratorCallbacks,
) error {
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	turnID := fmt.Sprintf("turn_%d", time.Now().UnixNano())
	st := newRunState(callbacks, r.config.StreamingMode == adkagent.StreamingModeSSE, msgID, turnID)

	// 通知消息开始
	if callbacks.OnMessageStart != nil {
		callbacks.OnMessageStart(st.msgID, 0, turnID, "model")
	}

	events := r.runner.Run(ctx, userID, sessionID, content, adkagent.RunConfig{
		StreamingMode: r.config.StreamingMode,
	})

	for event, err := range events {
		if err != nil {
			st.setError("event_error", err.Error())
			continue
		}

		// 流式模式下 partial 事件是逐块增量（文本/思考/FC），仅用于前端展示，
		// 不参与迭代计数、消息拆分、工具结果与 token 用量处理。
		// final（聚合）事件与非流式事件一一对应 → 迭代计数语义完全一致。
		isPartial := event.LLMResponse.Partial

		// MaxIterations 只统计 non-partial 事件：流式下每个 chunk 都是 partial，
		// 若按事件数计数会被增量块撑爆提前截断（默认 50）。
		if !isPartial {
			st.iterCount++
			if r.config.MaxIterations > 0 && st.iterCount > r.config.MaxIterations {
				st.setError("max_iterations", "达到最大迭代次数限制")
				break
			}
		}

		if event.Content == nil {
			continue
		}

		// 按子响应拆分消息：每个新的模型响应开启一条新消息，
		// 与历史消息（每条 = 一段思考 + 若干工具调用）保持一致。
		// 工具结果事件不拆分，归入发起该调用所在子响应的消息。
		// 仅 non-partial 事件参与拆分（partial 增量块属于当前消息）。
		if isModelEvent(event.Content) {
			if !isPartial && st.msgHasModelParts {
				st.startNextMessage()
			}
			if !isPartial {
				st.msgHasModelParts = true
			}
		}

		// 处理 Content Parts
		for _, part := range event.Content.Parts {
			st.handlePart(part, isPartial)
		}

		// Token 用量：仅 non-partial（final 聚合事件携带完整 usage；partial 无）
		if !isPartial && event.UsageMetadata != nil {
			st.handleTokenUsage(event.UsageMetadata)
		}

		// 错误处理
		if event.ErrorCode != "" {
			st.setError(event.ErrorCode, event.ErrorMessage)
		}
	}

	// 通知消息结束
	if callbacks.OnMessageEnd != nil {
		callbacks.OnMessageEnd(st.msgID)
	}

	// 回合完成
	if callbacks.OnTurnComplete != nil && !st.hasError {
		callbacks.OnTurnComplete(turnID, sessionID)
	}

	// 运行失败：返回错误供调用方感知（错误已通过 OnError 回调精确上报）
	if st.hasError {
		return st.lastErr
	}
	return nil
}

// runState 单次 runContent 的共享状态，供各事件处理子方法使用。
type runState struct {
	callbacks        *OrchestratorCallbacks
	isStreaming      bool
	msgID            string
	turnID           string
	seq              int64
	msgHasModelParts bool
	iterCount        int
	hasError         bool
	lastErr          error
	totalContent     strings.Builder
	emittedCalls     map[string]bool
	emittedApprovals map[string]bool
}

func newRunState(callbacks *OrchestratorCallbacks, isStreaming bool, msgID, turnID string) *runState {
	return &runState{
		callbacks:        callbacks,
		isStreaming:      isStreaming,
		msgID:            msgID,
		turnID:           turnID,
		emittedCalls:     make(map[string]bool),
		emittedApprovals: make(map[string]bool),
	}
}

// setError 记录错误（含消息），并通过 callbacks.OnError 上报。
func (s *runState) setError(code, message string) {
	s.hasError = true
	s.lastErr = fmt.Errorf("%s: %s", code, message)
	if s.callbacks.OnError != nil {
		s.callbacks.OnError(s.msgID, code, message)
	}
}

// isModelEvent 判断事件是否为"模型响应"（含思考/文本/函数调用；工具结果与确认事件不算）。
// adk_request_confirmation 确认事件不视为模型响应（不拆新消息，避免空气泡）。
func isModelEvent(content *genai.Content) bool {
	for _, part := range content.Parts {
		if part == nil {
			continue
		}
		if part.Text != "" || (part.FunctionCall != nil && part.FunctionCall.Name != adktoolconfirmation.FunctionCallName) {
			return true
		}
	}
	return false
}

// startNextMessage 结束当前消息并开启下一条（子响应拆分）。
func (s *runState) startNextMessage() {
	if s.callbacks.OnMessageEnd != nil {
		s.callbacks.OnMessageEnd(s.msgID)
	}
	s.seq++
	s.msgID = fmt.Sprintf("msg_%d_%d", time.Now().UnixNano(), s.seq)
	if s.callbacks.OnMessageStart != nil {
		s.callbacks.OnMessageStart(s.msgID, s.seq, s.turnID, "model")
	}
}

// handlePart 处理单个 Content Part：文本/思考、FunctionCall（含审批）、FunctionResponse。
func (s *runState) handlePart(part *genai.Part, isPartial bool) {
	if part == nil {
		return
	}

	// 文本/思考：流式模式下只转发 partial 增量块；final（聚合）事件含完整文本
	//（与增量块重复），跳过避免前端重复渲染。非流式模式所有事件都转发。
	if part.Text != "" {
		if s.isStreaming && !isPartial {
			return
		}
		if part.Thought && s.callbacks.OnReasoningDelta != nil {
			s.callbacks.OnReasoningDelta(s.msgID, part.Text)
		} else if !part.Thought {
			s.totalContent.WriteString(part.Text)
			if s.callbacks.OnContentDelta != nil {
				s.callbacks.OnContentDelta(s.msgID, part.Text)
			}
		}
	}

	// FunctionCall（工具调用）
	if part.FunctionCall != nil {
		// ADK 原生 HITL：识别 adk_request_confirmation 事件 → 触发审批请求（approvalID=确认 FC 的 ID）
		if part.FunctionCall.Name == adktoolconfirmation.FunctionCallName {
			s.handleApproval(part.FunctionCall)
			return
		}
		s.handleFunctionCall(part.FunctionCall)
	}

	// FunctionResponse（工具执行结果）
	if part.FunctionResponse != nil {
		// 流式下 FR 恒为 final 事件；partial 增量块不含 FR，防御性跳过
		if isPartial {
			return
		}
		s.handleFunctionResponse(part.FunctionResponse)
	}
}

// handleApproval 处理 adk_request_confirmation 确认事件 → 触发审批请求。
// 流式下 partial 与 final 各出现一次确认 FC，去重避免重复触发审批。
func (s *runState) handleApproval(fc *genai.FunctionCall) {
	if s.emittedApprovals[fc.ID] {
		return
	}
	s.emittedApprovals[fc.ID] = true

	if s.callbacks.OnApprovalRequired == nil {
		log.Printf("[ADK] 识别确认事件但 OnApprovalRequired 未注册（approvalID=%s）", fc.ID)
		return
	}

	approvalID := fc.ID
	origCallID := approvalID
	command := ""
	risk := ftool.RiskDangerous
	if orig, err := adktoolconfirmation.OriginalCallFrom(fc); err == nil && orig != nil {
		origCallID = orig.ID
		// 命令工具只展示实际命令，不暴露内部参数（command/goal/explanation 等）
		if orig.Name == "run_command" {
			if cmd, ok := orig.Args["command"].(string); ok {
				command = cmd
			}
		} else if orig.Args != nil {
			if b, jerr := json.Marshal(orig.Args); jerr == nil {
				command = fmt.Sprintf("%s %s", orig.Name, string(b))
			} else {
				command = fmt.Sprintf("%s %v", orig.Name, orig.Args)
			}
		} else {
			command = orig.Name
		}
	}
	// 从 toolConfirmation 提取 hint 与 payload（含 risk）
	if tc, ok := fc.Args["toolConfirmation"].(map[string]any); ok {
		if p, ok := tc["payload"].(map[string]any); ok {
			if r, ok := p["risk"].(string); ok && r != "" {
				risk = ftool.RiskLevel(r)
			}
		}
	}
	log.Printf("[ADK] 识别确认事件 → approval_required approvalID=%s callID=%s risk=%s cmd=%q", approvalID, origCallID, risk, command)
	s.callbacks.OnApprovalRequired(approvalID, origCallID, command, risk)
}

// handleFunctionCall 上报工具调用（流式去重：partial 先到并转发，final 聚合事件重复时跳过）。
func (s *runState) handleFunctionCall(fc *genai.FunctionCall) {
	callID := fc.ID
	if callID == "" {
		callID = fmt.Sprintf("call_%s_%d", fc.Name, time.Now().UnixNano())
	}
	if s.emittedCalls[callID] {
		return
	}
	s.emittedCalls[callID] = true

	if s.callbacks.OnToolCallStart != nil {
		s.callbacks.OnToolCallStart(s.msgID, callID, fc.Name)
	}
	// ADK 一次性给全 args，直接发送完整结果
	if s.callbacks.OnToolCallEnd != nil {
		argsStr := ""
		if fc.Args != nil {
			if b, jerr := json.Marshal(fc.Args); jerr == nil {
				argsStr = string(b)
			} else {
				argsStr = fmt.Sprintf("%v", fc.Args)
			}
		}
		s.callbacks.OnToolCallEnd(callID, fc.Name, argsStr)
	}
}

// handleFunctionResponse 上报工具执行结果。
func (s *runState) handleFunctionResponse(fr *genai.FunctionResponse) {
	callID := fr.ID
	name := fr.Name
	respStr := fmt.Sprintf("%v", fr.Response)
	// 跳过 HITL 占位 FR（确认等待中的占位结果，含 "requires confirmation"），避免误报为真实工具结果
	if strings.Contains(respStr, confirmationPlaceholder) {
		return
	}
	result := &ftool.ToolResult{
		Success: true,
		Output:  respStr,
	}
	// 工具返回错误（如审批被拒绝 call is rejected）→ 标记失败，前端据此显示 error 状态
	if respMap := fr.Response; respMap != nil {
		if errStr, ok := respMap["error"].(string); ok && errStr != "" {
			result.Success = false
			result.Error = errStr
			result.Output = errStr
		}
	}
	log.Printf("[ADK] tool_result callID=%s name=%s success=%v output=%q", callID, name, result.Success, result.Output)

	if s.callbacks.OnToolExecuting != nil {
		s.callbacks.OnToolExecuting(callID, name)
	}
	toolMsgID := fmt.Sprintf("tool_%s_%d", callID, time.Now().UnixNano())
	if s.callbacks.OnToolResult != nil {
		s.callbacks.OnToolResult(callID, toolMsgID, result)
	}
}

// handleTokenUsage 上报 token 用量。
func (s *runState) handleTokenUsage(usage *genai.GenerateContentResponseUsageMetadata) {
	if s.callbacks.OnTokenUsage == nil {
		return
	}
	s.callbacks.OnTokenUsage(ftool.TokenUsageInfo{
		PromptTokens:     int(usage.PromptTokenCount),
		CompletionTokens: int(usage.CandidatesTokenCount),
		TotalTokens:      int(usage.TotalTokenCount),
		CachedTokens:     int(usage.CachedContentTokenCount),
	})
}
