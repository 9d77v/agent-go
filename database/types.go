package database

import (
	"encoding/json"
	"time"
)

// Message is the generic message struct returned by ListHistory.
// This replaces msgdb.Message after the msgdb removal.
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

// Session is the generic session struct returned by ListSessions.
// This replaces msgdb.Session after the msgdb removal.
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
