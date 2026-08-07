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

	// MemoryService 记忆服务（可选）。
	MemoryService any

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
	// 生成消息 ID 和回合 ID
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	turnID := fmt.Sprintf("turn_%d", time.Now().UnixNano())
	seq := int64(0)

	// 通知消息开始
	if callbacks.OnMessageStart != nil {
		callbacks.OnMessageStart(msgID, seq, turnID, "model")
	}

	events := r.runner.Run(ctx, userID, sessionID, content, adkagent.RunConfig{
		StreamingMode: r.config.StreamingMode,
	})

	var totalContent strings.Builder
	iterCount := 0
	hasError := false
	msgHasModelParts := false
	// 流式去重：partial（增量块）与 final（聚合事件）会携带相同的 FC / 审批确认事件，
	// 记录已上报的 callID / approvalID，避免前端重复渲染。
	emittedCalls := make(map[string]bool)
	emittedApprovals := make(map[string]bool)
	// 流式模式标志：final（聚合）事件的文本/思考跳过（partial 增量已逐块转发），
	// 非流式模式所有事件都转发。
	isStreaming := r.config.StreamingMode == adkagent.StreamingModeSSE

	for event, err := range events {
		if err != nil {
			hasError = true
			if callbacks.OnError != nil {
				callbacks.OnError(msgID, "event_error", err.Error())
			}
			continue
		}

		// 流式模式下 partial 事件是逐块增量（文本/思考/FC），仅用于前端展示，
		// 不参与迭代计数、消息拆分、工具结果与 token 用量处理。
		// final（聚合）事件与非流式事件一一对应 → 迭代计数语义完全一致。
		isPartial := event.LLMResponse.Partial

		// MaxIterations 只统计 non-partial 事件：流式下每个 chunk 都是 partial，
		// 若按事件数计数会被增量块撑爆提前截断（默认 50）。
		if !isPartial {
			iterCount++
			if r.config.MaxIterations > 0 && iterCount > r.config.MaxIterations {
				hasError = true
				if callbacks.OnError != nil {
					callbacks.OnError(msgID, "max_iterations", "达到最大迭代次数限制")
				}
				break
			}
		}

		if event.Content == nil {
			continue
		}

		// 判断该事件是否为"模型响应"（含思考/文本/函数调用）；工具结果事件不含这些。
		// adk_request_confirmation 确认事件不视为模型响应（不拆新消息，避免空气泡）。
		isModelEvent := false
		for _, part := range event.Content.Parts {
			if part == nil {
				continue
			}
			if part.Text != "" || (part.FunctionCall != nil && part.FunctionCall.Name != adktoolconfirmation.FunctionCallName) {
				isModelEvent = true
				break
			}
		}

		// 按子响应拆分消息：每个新的模型响应开启一条新消息，
		// 与历史消息（每条 = 一段思考 + 若干工具调用）保持一致。
		// 工具结果事件不拆分，归入发起该调用所在子响应的消息。
		// 仅 non-partial 事件参与拆分（partial 增量块属于当前消息）。
		if !isPartial && isModelEvent && msgHasModelParts {
			if callbacks.OnMessageEnd != nil {
				callbacks.OnMessageEnd(msgID)
			}
			seq++
			msgID = fmt.Sprintf("msg_%d_%d", time.Now().UnixNano(), seq)
			if callbacks.OnMessageStart != nil {
				callbacks.OnMessageStart(msgID, seq, turnID, "model")
			}
		}
		if !isPartial && isModelEvent {
			msgHasModelParts = true
		}

		// 处理 Content Parts
		for _, part := range event.Content.Parts {
			// 文本/思考：流式模式下只转发 partial 增量块；final（聚合）事件含完整文本
			//（与增量块重复），跳过避免前端重复渲染。非流式模式所有事件都转发。
			if part.Text != "" && isStreaming && !isPartial {
				continue
			}
			// 思考/推理内容
			if part.Text != "" && part.Thought && callbacks.OnReasoningDelta != nil {
				callbacks.OnReasoningDelta(msgID, part.Text)
			} else if part.Text != "" && !part.Thought {
				totalContent.WriteString(part.Text)
				if callbacks.OnContentDelta != nil {
					callbacks.OnContentDelta(msgID, part.Text)
				}
			}

			// FunctionCall（工具调用）
			if part.FunctionCall != nil {
				// ADK 原生 HITL：识别 adk_request_confirmation 事件 → 触发审批请求（approvalID=确认 FC 的 ID）
				if part.FunctionCall.Name == adktoolconfirmation.FunctionCallName {
					// 流式下 partial 与 final 各出现一次确认 FC，去重避免重复触发审批
					if emittedApprovals[part.FunctionCall.ID] {
						continue
					}
					emittedApprovals[part.FunctionCall.ID] = true
					if callbacks.OnApprovalRequired != nil {
						approvalID := part.FunctionCall.ID
						origCallID := approvalID
						command := ""
						risk := ftool.RiskDangerous
						if orig, err := adktoolconfirmation.OriginalCallFrom(part.FunctionCall); err == nil && orig != nil {
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
						if tc, ok := part.FunctionCall.Args["toolConfirmation"].(map[string]any); ok {
							if p, ok := tc["payload"].(map[string]any); ok {
								if r, ok := p["risk"].(string); ok && r != "" {
									risk = ftool.RiskLevel(r)
								}
							}
						}
						log.Printf("[ADK] 识别确认事件 → approval_required approvalID=%s callID=%s risk=%s cmd=%q", approvalID, origCallID, risk, command)
						callbacks.OnApprovalRequired(approvalID, origCallID, command, risk)
					} else {
						log.Printf("[ADK] 识别确认事件但 OnApprovalRequired 未注册（approvalID=%s）", part.FunctionCall.ID)
					}
					// 不当作普通工具调用上报
					continue
				}

				callID := part.FunctionCall.ID
				if callID == "" {
					callID = fmt.Sprintf("call_%s_%d", part.FunctionCall.Name, time.Now().UnixNano())
				}
				// 流式下去重：partial 先到并转发，final 聚合事件重复时跳过
				if emittedCalls[callID] {
					continue
				}
				emittedCalls[callID] = true
				name := part.FunctionCall.Name
				argsJSON := part.FunctionCall.Args

				if callbacks.OnToolCallStart != nil {
					callbacks.OnToolCallStart(msgID, callID, name)
				}
				// ADK 一次性给全 args，直接发送完整结果
				if callbacks.OnToolCallEnd != nil {
					argsStr := ""
					if argsJSON != nil {
						if b, jerr := json.Marshal(argsJSON); jerr == nil {
							argsStr = string(b)
						} else {
							argsStr = fmt.Sprintf("%v", argsJSON)
						}
					}
					callbacks.OnToolCallEnd(callID, name, argsStr)
				}
			}

			// FunctionResponse（工具执行结果）
			if part.FunctionResponse != nil {
				// 流式下 FR 恒为 final 事件；partial 增量块不含 FR，防御性跳过
				if isPartial {
					continue
				}
				callID := part.FunctionResponse.ID
				name := part.FunctionResponse.Name
				respStr := fmt.Sprintf("%v", part.FunctionResponse.Response)
				// 跳过 HITL 占位 FR（确认等待中的占位结果，含 "requires confirmation"），避免误报为真实工具结果
				if strings.Contains(respStr, "requires confirmation") {
					continue
				}
				result := &ftool.ToolResult{
					Success: true,
					Output:  respStr,
				}
				// 工具返回错误（如审批被拒绝 call is rejected）→ 标记失败，前端据此显示 error 状态
				if respMap := part.FunctionResponse.Response; respMap != nil {
					if errStr, ok := respMap["error"].(string); ok && errStr != "" {
						result.Success = false
						result.Error = errStr
						result.Output = errStr
					}
				}
				log.Printf("[ADK] tool_result callID=%s name=%s success=%v output=%q", callID, name, result.Success, result.Output)

				if callbacks.OnToolExecuting != nil {
					callbacks.OnToolExecuting(callID, name)
				}
				toolMsgID := fmt.Sprintf("tool_%s_%d", callID, time.Now().UnixNano())
				if callbacks.OnToolResult != nil {
					callbacks.OnToolResult(callID, toolMsgID, result)
				}
				// 工具结果事件不拆分消息，归入发起该调用所在子响应的消息。
			}
		}

		// Token 用量：仅 non-partial（final 聚合事件携带完整 usage；partial 无）
		if !isPartial && event.UsageMetadata != nil && callbacks.OnTokenUsage != nil {
			callbacks.OnTokenUsage(ftool.TokenUsageInfo{
				PromptTokens:     int(event.UsageMetadata.PromptTokenCount),
				CompletionTokens: int(event.UsageMetadata.CandidatesTokenCount),
				TotalTokens:      int(event.UsageMetadata.TotalTokenCount),
				CachedTokens:     int(event.UsageMetadata.CachedContentTokenCount),
			})
		}

		// 错误处理
		if event.ErrorCode != "" && callbacks.OnError != nil {
			callbacks.OnError(msgID, event.ErrorCode, event.ErrorMessage)
		}
	}

	// 通知消息结束
	if callbacks.OnMessageEnd != nil {
		callbacks.OnMessageEnd(msgID)
	}

	// 回合完成
	if callbacks.OnTurnComplete != nil && !hasError {
		callbacks.OnTurnComplete(turnID, sessionID)
	}

	return nil
}
