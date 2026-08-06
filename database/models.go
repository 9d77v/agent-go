package database

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
)

// ── Common GORM models persisted in the main database ──

// SessionExtModel stores extended session data (approval_mode, title, token_usage, system_prompt)
// that ADK session.Service does not natively support.
type SessionExtModel struct {
	SessionID    string   `gorm:"primaryKey;size:64"`
	ApprovalMode string   `gorm:"size:16;default:default"`
	Title        string   `gorm:"size:256;default:''"`
	TokenUsage   string   `gorm:"type:text;default:'{}'"`
	SystemPrompt string   `gorm:"type:text;default:''"`
	Todos        TodoList `gorm:"type:text;default:'[]'"`
}

// TodoItem 单个待办项（与 todo 工具 todoList schema 对齐：id/title/status）
type TodoItem struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// TodoList 会话级待办清单，作为类型化 JSON 列存储。
// SQLite（glebarez 纯 Go 驱动）无原生 JSON 类型，通过 Value/Scan 以 JSON 文本落库。
type TodoList []TodoItem

// Value 实现 driver.Valuer，序列化为 JSON 文本。
func (t TodoList) Value() (driver.Value, error) {
	if t == nil {
		t = TodoList{}
	}
	return json.Marshal(t)
}

// Scan 实现 sql.Scanner，从 JSON 文本反序列化。
func (t *TodoList) Scan(v any) error {
	if v == nil {
		*t = TodoList{}
		return nil
	}
	var data []byte
	switch x := v.(type) {
	case []byte:
		data = x
	case string:
		data = []byte(x)
	default:
		return fmt.Errorf("无法解析 TodoList: %T", v)
	}
	if len(data) == 0 {
		*t = TodoList{}
		return nil
	}
	return json.Unmarshal(data, t)
}

func (SessionExtModel) TableName() string { return "session_ext" }

// CommonModels returns all framework-level GORM models.
// Business layers should append their own models when calling AutoMigrate.
func CommonModels() []any {
	return []any{&SessionExtModel{}, &BlobFile{}, &Artifact{}}
}

// MigrateCommon runs AutoMigrate for framework-level tables.
// Business layers should call this with their own db, then add business tables.
func MigrateCommon(db *gorm.DB) error {
	return db.AutoMigrate(CommonModels()...)
}

// ── Read-only models for ADK's adk_sessions.db ──

// adkSession mirrors ADK's sessions table for listing.
type adkSession struct {
	ID        string `gorm:"column:id;primaryKey"`
	AppName   string `gorm:"column:app_name"`
	UserID    string `gorm:"column:user_id"`
	State     []byte `gorm:"column:state"`
	CreatedAt string `gorm:"column:create_time"`
	UpdatedAt string `gorm:"column:update_time"`
}

func (adkSession) TableName() string { return "sessions" }

// adkEvent mirrors ADK's events table for reading history.
type adkEvent struct {
	ID        string `gorm:"column:id;primaryKey"`
	SessionID string `gorm:"column:session_id;index"`
	Author    string `gorm:"column:author"`
	Content   []byte `gorm:"column:content"`
	Timestamp string `gorm:"column:timestamp"`
}

func (adkEvent) TableName() string { return "events" }
