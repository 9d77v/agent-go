package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	ftool "github.com/9d77v/agent-go/tool"
)

// streamChannelBufferSize 流消息通道的缓冲大小。
const streamChannelBufferSize = 200

// StreamManager manages active streaming sessions and dispatches
// orchestration callbacks to a message channel.
// Application-specific behavior (terminals, backups, etc.) should be
// added via wrapping or injection in the application layer.
type StreamManager struct {
	mu           sync.RWMutex
	streams      map[string]*StreamSession
	orchestrator Orchestrator

	// 审批攒批：sessionID → approvalID → 决策（nil=未决）。
	// 同一编排批次产生的多个审批全部已决后才统一恢复（ADK 需一次响应所有确认才能并发执行全部工具）。
	approvalMu  sync.Mutex
	pendingAppr map[string]map[string]*bool
}

// NewStreamManager creates a new StreamManager.
func NewStreamManager(orchestrator Orchestrator) *StreamManager {
	return &StreamManager{
		streams:      make(map[string]*StreamSession),
		orchestrator: orchestrator,
		pendingAppr:  make(map[string]map[string]*bool),
	}
}

// resetApprovals 清空某 session 的待审批（新编排轮开始，旧批失效）。
func (sm *StreamManager) resetApprovals(sessionID string) {
	sm.approvalMu.Lock()
	delete(sm.pendingAppr, sessionID)
	sm.approvalMu.Unlock()
}

// recordApproval 记录某 session 产生的审批请求（未决）。
func (sm *StreamManager) recordApproval(sessionID, approvalID string) {
	sm.approvalMu.Lock()
	batch := sm.pendingAppr[sessionID]
	if batch == nil {
		batch = make(map[string]*bool)
		sm.pendingAppr[sessionID] = batch
	}
	batch[approvalID] = nil
	sm.approvalMu.Unlock()
}

// ResolveApproval 记录单个审批决策；当同批所有审批都已决时返回应一次恢复的决策列表。
// 返回值：decisions=待恢复决策；resume=true 表示应启动恢复流；ok=false 表示该审批未知（已恢复或超时）。
func (sm *StreamManager) ResolveApproval(sessionID, approvalID string, approved bool) (decisions []ApprovalDecision, resume, ok bool) {
	sm.approvalMu.Lock()
	defer sm.approvalMu.Unlock()
	batch := sm.pendingAppr[sessionID]
	if batch == nil {
		return nil, false, false
	}
	if _, exists := batch[approvalID]; !exists {
		return nil, false, false
	}
	v := approved
	batch[approvalID] = &v
	// 同批还有未决 → 继续等待（不逐个恢复，避免剩余确认永远挂起导致编排卡死）
	for _, d := range batch {
		if d == nil {
			return nil, false, true
		}
	}
	decisions = make([]ApprovalDecision, 0, len(batch))
	for id, d := range batch {
		decisions = append(decisions, ApprovalDecision{ApprovalID: id, Approved: *d})
	}
	delete(sm.pendingAppr, sessionID)
	return decisions, true, true
}

// StartStream starts a new streaming session.
// Returns streamID, message channel, and error.
func (sm *StreamManager) StartStream(
	sessionID, message string,
	images []ImageInput,
	model, providerID, mode, thinking, approvalMode string,
	includeProjectDocs ...bool,
) (string, <-chan StreamMessage, error) {
	ctx, cancel := context.WithCancel(context.Background())

	streamID := fmt.Sprintf("stream_%d", time.Now().UnixNano())
	msgCh := make(chan StreamMessage, streamChannelBufferSize)

	ss := &StreamSession{
		ID:        streamID,
		SessionID: sessionID,
		Cancel:    cancel,
		CreatedAt: time.Now().UnixMilli(),
	}

	sm.mu.Lock()
	sm.streams[streamID] = ss
	sm.mu.Unlock()

	go func() {
		defer close(msgCh)
		defer func() {
			sm.mu.Lock()
			delete(sm.streams, streamID)
			sm.mu.Unlock()
		}()

		// 新编排轮开始：清空该 session 旧批审批（旧批随上一轮暂停/结束已失效）
		sm.resetApprovals(sessionID)

		callbacks := newStreamCallbacks(msgCh, ctx)
		// 记录审批请求归属的 session（攒批：等同批全部已决后统一恢复）
		if cb := callbacks.OnApprovalRequired; cb != nil {
			callbacks.OnApprovalRequired = func(approvalID, callID, command string, risk ftool.RiskLevel) {
				sm.recordApproval(sessionID, approvalID)
				cb(approvalID, callID, command, risk)
			}
		}

		// 审核模式原始字符串直传（不在框架层 Parse，交给应用层 decider 处理语义）
		sm.orchestrator.Orchestrate(ctx, OrchestrateParams{
			SessionID:          sessionID,
			Message:            message,
			Images:             images,
			Model:              model,
			ProviderID:         providerID,
			Mode:               mode,
			Thinking:           thinking,
			ApprovalMode:       approvalMode,
			IncludeProjectDocs: len(includeProjectDocs) > 0 && includeProjectDocs[0],
		}, callbacks)
	}()

	return streamID, msgCh, nil
}

// StartResumeStream 启动审批恢复流（ADK 原生 HITL）：以 FunctionResponse 恢复被暂停的工具。
// decisions 为同批全部已决的审批（等一批都通过后由 ResolveApproval 攒齐），一次提交多个确认响应，
// ADK 会并发执行所有被确认的工具。参数由应用层 pendingApproval 记录补齐（model/mode 等）。
func (sm *StreamManager) StartResumeStream(
	sessionID string,
	decisions []ApprovalDecision,
) (string, <-chan StreamMessage, error) {
	ctx, cancel := context.WithCancel(context.Background())

	streamID := fmt.Sprintf("stream_%d", time.Now().UnixNano())
	msgCh := make(chan StreamMessage, streamChannelBufferSize)

	ss := &StreamSession{
		ID:        streamID,
		SessionID: sessionID,
		Cancel:    cancel,
		CreatedAt: time.Now().UnixMilli(),
	}

	sm.mu.Lock()
	sm.streams[streamID] = ss
	sm.mu.Unlock()

	go func() {
		defer close(msgCh)
		defer func() {
			sm.mu.Lock()
			delete(sm.streams, streamID)
			sm.mu.Unlock()
		}()

		callbacks := newStreamCallbacks(msgCh, ctx)
		// 恢复流内模型可能再次触发审批（链式审批）→ 同样记录，构成新一批
		if cb := callbacks.OnApprovalRequired; cb != nil {
			callbacks.OnApprovalRequired = func(approvalID, callID, command string, risk ftool.RiskLevel) {
				sm.recordApproval(sessionID, approvalID)
				cb(approvalID, callID, command, risk)
			}
		}
		sm.orchestrator.ResumeOrchestrate(ctx, ResumeParams{
			SessionID: sessionID,
			Approvals: decisions,
		}, callbacks)
	}()

	return streamID, msgCh, nil
}

// newStreamCallbacks 构建默认的 stream → msgCh 回调转发器。
func newStreamCallbacks(msgCh chan<- StreamMessage, ctx context.Context) *OrchestratorCallbacks {
	return &OrchestratorCallbacks{
		OnMessageStart: func(msgID string, seq int64, turnID, role string) {
			select {
			case msgCh <- StreamMessage{Type: "message_start", MsgID: msgID, Seq: seq, TurnID: turnID, Content: role}:
			case <-ctx.Done():
			}
		},
		OnContentDelta: func(msgID, delta string) {
			select {
			case msgCh <- StreamMessage{Type: "content_delta", MsgID: msgID, Delta: delta}:
			case <-ctx.Done():
			}
		},
		OnReasoningDelta: func(msgID, delta string) {
			select {
			case msgCh <- StreamMessage{Type: "reasoning_delta", MsgID: msgID, Delta: delta}:
			case <-ctx.Done():
			}
		},
		OnMessageEnd: func(msgID string) {
			select {
			case msgCh <- StreamMessage{Type: "message_end", MsgID: msgID}:
			case <-ctx.Done():
			}
		},
		OnToolCallStart: func(msgID, callID, name string) {
			select {
			case msgCh <- StreamMessage{Type: "tool_call_start", MsgID: msgID, CallID: callID, ToolName: name}:
			case <-ctx.Done():
			}
		},
		OnToolCallDelta: func(callID, argsDelta string) {
			select {
			case msgCh <- StreamMessage{Type: "tool_call_delta", CallID: callID, Delta: argsDelta}:
			case <-ctx.Done():
			}
		},
		OnToolCallEnd: func(callID, name, arguments string) {
			select {
			case msgCh <- StreamMessage{Type: "tool_call_end", CallID: callID, ToolName: name, ToolArgs: arguments}:
			case <-ctx.Done():
			}
		},
		OnToolExecuting: func(callID, name string) {
			select {
			case msgCh <- StreamMessage{Type: "tool_executing", CallID: callID, ToolName: name}:
			case <-ctx.Done():
			}
		},
		OnToolResult: func(callID, toolMsgID string, result *ftool.ToolResult) {
			resultJSON, _ := json.Marshal(result)
			select {
			case msgCh <- StreamMessage{Type: "tool_result", CallID: callID, ToolMsgID: toolMsgID, ToolArgs: string(resultJSON)}:
			case <-ctx.Done():
			}
		},
		OnApprovalRequired: func(approvalID, callID, command string, risk ftool.RiskLevel) {
			log.Printf("[Stream] approval_required → ws approvalID=%s callID=%s risk=%s", approvalID, callID, risk)
			select {
			case msgCh <- StreamMessage{Type: "approval_required", ApprovalID: approvalID, CallID: callID, Content: command, RiskLevel: string(risk)}:
			case <-ctx.Done():
			}
		},
		OnQuestionnaire: func(questionnaireID, questionsJSON string) {
			select {
			case msgCh <- StreamMessage{Type: "questionnaire_request", QuestionnaireID: questionnaireID, ToolArgs: questionsJSON}:
			case <-ctx.Done():
			}
		},
		OnTokenUsage: func(usage ftool.TokenUsageInfo) {
			usageData, _ := json.Marshal(usage)
			select {
			case msgCh <- StreamMessage{Type: "token_usage", ToolArgs: string(usageData)}:
			case <-ctx.Done():
			}
		},
		OnTurnComplete: func(turnID, sessionID string) {
			select {
			case msgCh <- StreamMessage{Type: "turn_complete", TurnID: turnID, SessionID: sessionID}:
			case <-ctx.Done():
			}
		},
		OnError: func(msgID, code, message string) {
			select {
			case msgCh <- StreamMessage{Type: "error", MsgID: msgID, Code: code, Error: message}:
			case <-ctx.Done():
			}
		},
		OnTerminalOutput: func(text string, isStderr bool) {
			select {
			case msgCh <- StreamMessage{Type: "terminal_output", ToolArgs: text, IsStderr: isStderr}:
			case <-ctx.Done():
			}
		},
	}
}

// CancelStream cancels a streaming session by ID.
func (sm *StreamManager) CancelStream(streamID string) {
	sm.mu.RLock()
	ss, ok := sm.streams[streamID]
	sm.mu.RUnlock()
	if ok && ss.Cancel != nil {
		ss.Cancel()
	}
}

// ResolveQuestionnaire resolves a questionnaire request.
func (sm *StreamManager) ResolveQuestionnaire(questionnaireID, answersJSON string) {
	log.Printf("[Stream] Questionnaire %s answered", questionnaireID)
}
