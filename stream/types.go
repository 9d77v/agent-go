// Package stream provides generic WebSocket streaming types and
// a StreamManager for agent-go based applications.
package stream

import (
	"context"

	fwagent "github.com/9d77v/agent-go/agent"
	ftool "github.com/9d77v/agent-go/tool"
)

// OrchestratorCallbacks re-exports the agent package callbacks.
type OrchestratorCallbacks = fwagent.OrchestratorCallbacks

// ImageInput 随 start 消息提交的图片（webp base64），复用 agent 包类型。
type ImageInput = fwagent.ImageInput

// Orchestrator is the interface for running an agent orchestration.
type Orchestrator interface {
	Orchestrate(
		ctx context.Context,
		sessionID, message string,
		images []ImageInput,
		model, providerID, mode, thinking string,
		approvalMode string,
		termID string,
		callbacks *OrchestratorCallbacks,
		includeProjectDocs ...bool,
	)
	// ResumeOrchestrate 审批恢复编排：ADK 原生 HITL 恢复轮，以 FunctionResponse 恢复被暂停的工具。
	ResumeOrchestrate(
		ctx context.Context,
		sessionID, approvalID string,
		approved bool,
		callbacks *OrchestratorCallbacks,
	)
}

// StreamMessage is a WebSocket message sent from backend to frontend.
// The 15 message types (started, message_start, content_delta, reasoning_delta,
// message_end, tool_call_start/delta/end, tool_executing, tool_result,
// approval_required, questionnaire_request, token_usage, turn_complete,
// error, terminal_output) are the protocol contract between agent-go and agent-ui.
type StreamMessage struct {
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
	FilePath        string `json:"file_path,omitempty"`
	IsStderr        bool   `json:"is_stderr,omitempty"`
	Approved        bool   `json:"approved,omitempty"`
	ApprovalID      string `json:"approval_id,omitempty"`
	QuestionnaireID string `json:"questionnaire_id,omitempty"`
	Text            string `json:"text,omitempty"`
	BackupPath      string `json:"backup_path,omitempty"`
	Error           string `json:"error,omitempty"`
	Code            string `json:"code,omitempty"`
	Mode            string `json:"mode,omitempty"`
	Thinking        string `json:"thinking,omitempty"`
	Model           string `json:"model,omitempty"`
	ProviderID      string `json:"provider_id,omitempty"`
	Message         string `json:"message,omitempty"`
	RiskLevel       string `json:"risk_level,omitempty"`
}

// WsRequest is a WebSocket client request.
type WsRequest struct {
	Type               string       `json:"type"`
	SessionID          string       `json:"session_id,omitempty"`
	StreamID           string       `json:"stream_id,omitempty"`
	Message            string       `json:"message,omitempty"`
	Images             []ImageInput `json:"images,omitempty"`
	Model              string       `json:"model,omitempty"`
	ProviderID         string       `json:"provider_id,omitempty"`
	Mode               string       `json:"mode,omitempty"`
	Thinking           string       `json:"thinking,omitempty"`
	ApprovalMode       string       `json:"approval_mode,omitempty"`
	IncludeProjectDocs bool         `json:"include_project_docs,omitempty"`
	ApprovalID         string       `json:"approval_id,omitempty"`
	Approved           bool         `json:"approved,omitempty"`
	FilePath           string       `json:"file_path,omitempty"`
	BackupPath         string       `json:"backup_path,omitempty"`
	QuestionnaireID    string       `json:"questionnaire_id,omitempty"`
	Text               string       `json:"text,omitempty"`
	MessageID          string       `json:"message_id,omitempty"`
}

// StreamSession manages a single streaming session.
type StreamSession struct {
	ID           string
	SessionID    string
	Cancel       context.CancelFunc
	CreatedAt    int64
	ApprovalMode ftool.ApprovalMode
}
