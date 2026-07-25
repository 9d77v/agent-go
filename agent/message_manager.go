package agent

import (
	"encoding/json"

	"github.com/google/uuid"

	"github.com/9d77v/agent-go/llm"
	"github.com/9d77v/agent-go/msgdb"
)

// MessageManager 集中管理消息的创建、持久化和历史重建
type MessageManager struct {
	store *msgdb.Store
}

// NewMessageManager 创建消息管理器
func NewMessageManager(store *msgdb.Store) *MessageManager {
	return &MessageManager{store: store}
}

// NewUserMessage 创建 user 消息
func (mm *MessageManager) NewUserMessage(sessionID, clientMsgID, turnID, content string) *msgdb.Message {
	return &msgdb.Message{
		ID:          uuid.Must(uuid.NewV7()).String(),
		SessionID:   sessionID,
		ClientMsgID: clientMsgID,
		TurnID:      turnID,
		Role:        "user",
		Content:     content,
	}
}

// NewAssistantMessage 创建 assistant 消息
func (mm *MessageManager) NewAssistantMessage(sessionID, turnID string) *msgdb.Message {
	return &msgdb.Message{
		ID:        uuid.Must(uuid.NewV7()).String(),
		SessionID: sessionID,
		TurnID:    turnID,
		Role:      "assistant",
	}
}

// NewToolMessage 创建 tool 消息
func (mm *MessageManager) NewToolMessage(sessionID, turnID, toolCallID, toolName, toolArgs, resultContent string) *msgdb.Message {
	tcJSON, _ := json.Marshal(map[string]any{
		"name":      toolName,
		"arguments": toolArgs,
		"id":        toolCallID,
	})
	return &msgdb.Message{
		ID:        uuid.Must(uuid.NewV7()).String(),
		SessionID: sessionID,
		TurnID:    turnID,
		Role:      "tool",
		Content:   resultContent,
		ToolCall:  json.RawMessage(tcJSON),
	}
}

// Persist 持久化消息
func (mm *MessageManager) Persist(msg *msgdb.Message) error {
	return mm.store.InsertMessage(msg)
}

// RebuildChatMessages 从存储重建 LLM 格式的完整对话历史
func (mm *MessageManager) RebuildChatMessages(sessionID string) []llm.ChatMessage {
	msgs, err := mm.store.ListMessages(sessionID)
	if err != nil || len(msgs) == 0 {
		return nil
	}
	result := make([]llm.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "system" {
			continue
		}
		msg := llm.ChatMessage{Role: m.Role, Content: m.Content}
		if m.Reasoning != "" {
			msg.ReasoningContent = m.Reasoning
		}
		if len(m.ToolCall) > 0 {
			switch m.Role {
			case "assistant":
				var tcList []struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
					ID        string `json:"id,omitempty"`
				}
				if err := json.Unmarshal(m.ToolCall, &tcList); err == nil && len(tcList) > 0 {
					tcs := make([]llm.ToolCallInfo, 0, len(tcList))
					for _, tc := range tcList {
						tcs = append(tcs, llm.ToolCallInfo{
							ID:   tc.ID,
							Type: "function",
							Function: struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							}{Name: tc.Name, Arguments: tc.Arguments},
						})
					}
					msg.ToolCalls = tcs
				}
			case "tool":
				var tcEntry struct{ ID string `json:"id"` }
				if err := json.Unmarshal(m.ToolCall, &tcEntry); err == nil && tcEntry.ID != "" {
					msg.ToolCallID = tcEntry.ID
				}
			}
		}
		result = append(result, msg)
	}
	return result
}
