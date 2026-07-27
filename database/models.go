package database

import "gorm.io/gorm"

// ── Common GORM models persisted in the main database ──

// SessionExtModel stores extended session data (approval_mode, title, token_usage, system_prompt)
// that ADK session.Service does not natively support.
type SessionExtModel struct {
	SessionID    string `gorm:"primaryKey;size:64"`
	ApprovalMode string `gorm:"size:16;default:default"`
	Title        string `gorm:"size:256;default:''"`
	TokenUsage   string `gorm:"type:text;default:'{}'"`
	SystemPrompt string `gorm:"type:text;default:''"`
}

func (SessionExtModel) TableName() string { return "session_ext" }

// CommonModels returns all framework-level GORM models.
// Business layers should append their own models when calling AutoMigrate.
func CommonModels() []any {
	return []any{&SessionExtModel{}}
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
