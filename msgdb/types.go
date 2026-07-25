package msgdb

import (
	"encoding/json"
	"time"
)

// Message 通用消息结构体
type Message struct {
	ID          string          `json:"id"`
	SessionID   string          `json:"session_id"`
	Seq         int64           `json:"seq"`
	TurnID      string          `json:"turn_id"`
	ClientMsgID string          `json:"client_msg_id,omitempty"`
	Role        string          `json:"role"`
	Content     string          `json:"content"`
	Reasoning   string          `json:"reasoning,omitempty"`
	ToolCall    json.RawMessage `json:"tool_call,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// Session 通用会话结构体
type Session struct {
	ID               string    `json:"id"`
	SessionID        string    `json:"session_id"`
	Title            string    `json:"title"`
	ApprovalMode     string    `json:"approval_mode"`
	WorkspaceID      string    `json:"workspace_id"`
	TokenUsage       string    `json:"token_usage"`
	LLMLastSentMsgID string    `json:"llm_last_sent_msg_id"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// MessageMetadata 消息扩展数据
type MessageMetadata struct {
	RetryDone       bool          `json:"retry_done,omitempty"`
	FileDiff        *FileDiffMeta `json:"file_diff,omitempty"`
	RetrySessionID  string        `json:"retry_session_id,omitempty"`
	RetryMessage    string        `json:"retry_message,omitempty"`
	RetryModel      string        `json:"retry_model,omitempty"`
	RetryProviderID string        `json:"retry_provider_id,omitempty"`
	RetryMode       string        `json:"retry_mode,omitempty"`
	RetryThinking   string        `json:"retry_thinking,omitempty"`
	HideFromDisplay bool          `json:"hide_from_display,omitempty"`
}

// FileDiffMeta 文件变更信息
type FileDiffMeta struct {
	FilePath   string `json:"file_path"`
	Original   string `json:"original"`
	Modified   string `json:"modified"`
	BackupPath string `json:"backup_path"`
}

func (m *Message) ParseMetadata() *MessageMetadata {
	if len(m.Metadata) == 0 {
		return &MessageMetadata{}
	}
	var meta MessageMetadata
	if json.Unmarshal(m.Metadata, &meta) != nil {
		return &MessageMetadata{}
	}
	return &meta
}

func (m *Message) SetMetadata(meta *MessageMetadata) {
	data, _ := json.Marshal(meta)
	m.Metadata = json.RawMessage(data)
}
