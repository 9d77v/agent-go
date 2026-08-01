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

// StreamManager manages active streaming sessions and dispatches
// orchestration callbacks to a message channel.
// Application-specific behavior (terminals, backups, etc.) should be
// added via wrapping or injection in the application layer.
type StreamManager struct {
	mu           sync.RWMutex
	streams      map[string]*StreamSession
	orchestrator Orchestrator
}

// NewStreamManager creates a new StreamManager.
func NewStreamManager(orchestrator Orchestrator) *StreamManager {
	return &StreamManager{
		streams:      make(map[string]*StreamSession),
		orchestrator: orchestrator,
	}
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
	msgCh := make(chan StreamMessage, 200)
	am := ftool.ParseApprovalMode(approvalMode)

	ss := &StreamSession{
		ID:           streamID,
		SessionID:    sessionID,
		Cancel:       cancel,
		CreatedAt:    time.Now().UnixMilli(),
		ApprovalMode: am,
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

		callbacks := &OrchestratorCallbacks{
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

		sm.orchestrator.Orchestrate(ctx, sessionID, message, images, model, providerID, mode, thinking, am, "", callbacks, includeProjectDocs...)
	}()

	return streamID, msgCh, nil
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

// ResolveApproval resolves an approval request.
func (sm *StreamManager) ResolveApproval(approvalID string, approved bool) {
	sm.orchestrator.ResolveApproval(approvalID, approved)
}

// ResolveQuestionnaire resolves a questionnaire request.
func (sm *StreamManager) ResolveQuestionnaire(questionnaireID, answersJSON string) {
	log.Printf("[Stream] Questionnaire %s answered", questionnaireID)
}
