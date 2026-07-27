// Package stream 提供 WebSocket 流式消息适配器，
// 将 OrchestratorCallbacks 转换为前端 agent-ui 可消费的 JSON 消息。
package stream

import (
	"encoding/json"
	"sync"

	"github.com/9d77v/agent-go/agent"
	ftool "github.com/9d77v/agent-go/tool"
)

// MessageSender 发送 WebSocket 消息的接口（由调用方实现）。
type MessageSender interface {
	SendJSON(v any) error
}

// Adapter 将 OrchestratorCallbacks 转换为 15 种 WebSocket JSON 消息类型。
// 消息格式兼容 agent-ui 前端。
type Adapter struct {
	sender MessageSender
	mu     sync.Mutex
}

// NewAdapter 创建流式适配器。
func NewAdapter(sender MessageSender) *Adapter {
	return &Adapter{sender: sender}
}

// CreateCallbacks 创建 OrchestratorCallbacks，将所有回调转换为 WebSocket 消息。
func (a *Adapter) CreateCallbacks() *agent.OrchestratorCallbacks {
	return &agent.OrchestratorCallbacks{
		OnMessageStart:     a.onMessageStart,
		OnContentDelta:     a.onContentDelta,
		OnReasoningDelta:   a.onReasoningDelta,
		OnMessageEnd:       a.onMessageEnd,
		OnToolCallStart:    a.onToolCallStart,
		OnToolCallDelta:    a.onToolCallDelta,
		OnToolCallEnd:      a.onToolCallEnd,
		OnToolExecuting:    a.onToolExecuting,
		OnToolResult:       a.onToolResult,
		OnApprovalRequired: a.onApprovalRequired,
		OnQuestionnaire:    a.onQuestionnaire,
		OnTokenUsage:       a.onTokenUsage,
		OnTurnComplete:     a.onTurnComplete,
		OnError:            a.onError,
		OnTerminalOutput:   a.onTerminalOutput,
	}
}

// ---------- 消息类型常量 ----------

const (
	TypeStarted          = "started"
	TypeMessageStart     = "message_start"
	TypeContentDelta     = "content_delta"
	TypeReasoningDelta   = "reasoning_delta"
	TypeMessageEnd       = "message_end"
	TypeToolCallStart    = "tool_call_start"
	TypeToolCallDelta    = "tool_call_delta"
	TypeToolCallEnd      = "tool_call_end"
	TypeToolExecuting    = "tool_executing"
	TypeToolResult       = "tool_result"
	TypeApprovalRequired = "approval_required"
	TypeQuestionnaire    = "questionnaire_request"
	TypeTokenUsage       = "token_usage"
	TypeTurnComplete     = "turn_complete"
	TypeError            = "error"
	TypeTerminalOutput   = "terminal_output"
)

// ---------- 消息结构体 ----------

type streamMsg struct {
	Type            string `json:"type"`
	StreamID        string `json:"stream_id,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	MsgID           string `json:"msg_id,omitempty"`
	Seq             int64  `json:"seq,omitempty"`
	TurnID          string `json:"turn_id,omitempty"`
	CallID          string `json:"call_id,omitempty"`
	ToolMsgID       string `json:"tool_msg_id,omitempty"`
	Delta           string `json:"delta,omitempty"`
	Content         string `json:"content,omitempty"`
	Reasoning       string `json:"reasoning,omitempty"`
	ToolName        string `json:"tool_name,omitempty"`
	ToolArgs        string `json:"tool_args,omitempty"`
	ApprovalID      string `json:"approval_id,omitempty"`
	QuestionnaireID string `json:"questionnaire_id,omitempty"`
	Text            string `json:"text,omitempty"`
	IsStderr        bool   `json:"is_stderr,omitempty"`
	RiskLevel       string `json:"risk_level,omitempty"`
	Role            string `json:"role,omitempty"`
	Error           string `json:"error,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
}

func (a *Adapter) send(msg *streamMsg) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sender.SendJSON(msg)
}

func (a *Adapter) onMessageStart(msgID string, seq int64, turnID, role string) {
	a.send(&streamMsg{Type: TypeMessageStart, MsgID: msgID, Seq: seq, TurnID: turnID, Role: role})
}

func (a *Adapter) onContentDelta(msgID, delta string) {
	a.send(&streamMsg{Type: TypeContentDelta, MsgID: msgID, Delta: delta})
}

func (a *Adapter) onReasoningDelta(msgID, delta string) {
	a.send(&streamMsg{Type: TypeReasoningDelta, MsgID: msgID, Delta: delta})
}

func (a *Adapter) onMessageEnd(msgID string) {
	a.send(&streamMsg{Type: TypeMessageEnd, MsgID: msgID})
}

func (a *Adapter) onToolCallStart(msgID, callID, name string) {
	a.send(&streamMsg{Type: TypeToolCallStart, MsgID: msgID, CallID: callID, ToolName: name})
}

func (a *Adapter) onToolCallDelta(callID, argsDelta string) {
	a.send(&streamMsg{Type: TypeToolCallDelta, CallID: callID, Delta: argsDelta})
}

func (a *Adapter) onToolCallEnd(callID, name, arguments string) {
	a.send(&streamMsg{Type: TypeToolCallEnd, CallID: callID, ToolName: name, ToolArgs: arguments})
}

func (a *Adapter) onToolExecuting(callID, name string) {
	a.send(&streamMsg{Type: TypeToolExecuting, CallID: callID, ToolName: name})
}

func (a *Adapter) onToolResult(callID, toolMsgID string, result *ftool.ToolResult) {
	args := map[string]any{"success": result.Success}
	if result.Output != "" {
		args["output"] = result.Output
	}
	if result.Error != "" {
		args["error"] = result.Error
	}
	argsJSON, _ := json.Marshal(args)
	a.send(&streamMsg{Type: TypeToolResult, CallID: callID, ToolMsgID: toolMsgID, ToolArgs: string(argsJSON)})
}

func (a *Adapter) onApprovalRequired(approvalID, callID, command string, risk ftool.RiskLevel) {
	a.send(&streamMsg{
		Type:       TypeApprovalRequired,
		ApprovalID: approvalID,
		CallID:     callID,
		Content:    command,
		RiskLevel:  string(risk),
	})
}

func (a *Adapter) onQuestionnaire(questionnaireID, questionsJSON string) {
	a.send(&streamMsg{Type: TypeQuestionnaire, QuestionnaireID: questionnaireID, Text: questionsJSON})
}

func (a *Adapter) onTokenUsage(usage ftool.TokenUsageInfo) {
	usageJSON, _ := json.Marshal(usage)
	a.send(&streamMsg{Type: TypeTokenUsage, ToolArgs: string(usageJSON)})
}

func (a *Adapter) onTurnComplete(turnID, sessionID string) {
	a.send(&streamMsg{Type: TypeTurnComplete, TurnID: turnID, SessionID: sessionID})
}

func (a *Adapter) onError(msgID, code, message string) {
	a.send(&streamMsg{Type: TypeError, MsgID: msgID, ErrorCode: code, Error: message})
}

func (a *Adapter) onTerminalOutput(text string, isStderr bool) {
	a.send(&streamMsg{Type: TypeTerminalOutput, Text: text, IsStderr: isStderr})
}
