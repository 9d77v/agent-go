// Package agent provides the ADK-Go wrapper for Wails desktop applications.
package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	ftool "github.com/9d77v/agent-go/tool"
)

// AdkRunnerConfig 配置 ADK Runner 的参数。
type AdkRunnerConfig struct {
	// AppName 应用名称，用于 session/memory 隔离。
	AppName string

	// Agent ADK Agent 实例（由调用方通过 llmagent.New 等创建）。
	Agent agent.Agent

	// SessionService 会话持久化服务。
	SessionService session.Service

	// MemoryService 记忆服务（可选）。
	MemoryService any

	// MaxIterations 最大迭代次数限制（0 表示不限制）。
	MaxIterations int
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

	r, err := runner.New(runner.Config{
		AppName:           cfg.AppName,
		Agent:             cfg.Agent,
		SessionService:    cfg.SessionService,
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

// Run 运行 Agent 对话，将 ADK Event 流转换为 OrchestratorCallbacks 回调。
func (r *AdkRunner) Run(
	ctx context.Context,
	userID, sessionID, message string,
	callbacks *OrchestratorCallbacks,
) error {
	content := genai.NewContentFromText(message, "user")

	// 生成消息 ID 和回合 ID
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	turnID := fmt.Sprintf("turn_%d", time.Now().UnixNano())
	seq := int64(0)

	// 通知消息开始
	if callbacks.OnMessageStart != nil {
		callbacks.OnMessageStart(msgID, seq, turnID, "model")
	}

	events := r.runner.Run(ctx, userID, sessionID, content, agent.RunConfig{})

	var totalContent strings.Builder
	iterCount := 0
	hasError := false

	for event, err := range events {
		if err != nil {
			hasError = true
			if callbacks.OnError != nil {
				callbacks.OnError(msgID, "event_error", err.Error())
			}
			continue
		}

		iterCount++
		if r.config.MaxIterations > 0 && iterCount > r.config.MaxIterations {
			if callbacks.OnError != nil {
				callbacks.OnError(msgID, "max_iterations", "达到最大迭代次数限制")
			}
			break
		}

		if event.Content == nil {
			continue
		}

		// 处理 Content Parts
		for _, part := range event.Content.Parts {
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
				callID := part.FunctionCall.ID
				if callID == "" {
					callID = fmt.Sprintf("call_%s_%d", part.FunctionCall.Name, time.Now().UnixNano())
				}
				name := part.FunctionCall.Name
				argsJSON := part.FunctionCall.Args

				if callbacks.OnToolCallStart != nil {
					callbacks.OnToolCallStart(msgID, callID, name)
				}
				// ADK 一次性给全 args，直接发送完整结果
				if callbacks.OnToolCallEnd != nil {
					argsStr := ""
					if argsJSON != nil {
						argsStr = fmt.Sprintf("%v", argsJSON)
					}
					callbacks.OnToolCallEnd(callID, name, argsStr)
				}
			}

			// FunctionResponse（工具执行结果）
			if part.FunctionResponse != nil {
				callID := part.FunctionResponse.ID
				name := part.FunctionResponse.Name
				result := &ftool.ToolResult{
					Success: true,
					Output:  fmt.Sprintf("%v", part.FunctionResponse.Response),
				}

				if callbacks.OnToolExecuting != nil {
					callbacks.OnToolExecuting(callID, name)
				}
				toolMsgID := fmt.Sprintf("tool_%s_%d", callID, time.Now().UnixNano())
				if callbacks.OnToolResult != nil {
					callbacks.OnToolResult(callID, toolMsgID, result)
				}

				// 工具结果发完后再关闭旧消息、开启新消息
				if callbacks.OnMessageEnd != nil {
					callbacks.OnMessageEnd(msgID)
				}
				seq++
				msgID = fmt.Sprintf("msg_%d_%d", time.Now().UnixNano(), seq)
				if callbacks.OnMessageStart != nil {
					callbacks.OnMessageStart(msgID, seq, turnID, "model")
				}
			}
		}

		// Token 用量
		if event.UsageMetadata != nil && callbacks.OnTokenUsage != nil {
			callbacks.OnTokenUsage(ftool.TokenUsageInfo{
				PromptTokens:     int(event.UsageMetadata.PromptTokenCount),
				CompletionTokens: int(event.UsageMetadata.CandidatesTokenCount),
				TotalTokens:      int(event.UsageMetadata.TotalTokenCount),
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
