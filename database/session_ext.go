package database

import "gorm.io/gorm"

// SessionExtStore provides GORM-persisted extended session data
// (title, token_usage, approval_mode, system_prompt) that ADK session.Service does not natively support.
type SessionExtStore struct {
	db *gorm.DB
}

// NewSessionExtStore creates a GORM-backed session extension store.
func NewSessionExtStore(db *gorm.DB) *SessionExtStore {
	return &SessionExtStore{db: db}
}

// GetSessionExt returns the session extension record, creating a default one if not exists.
func (s *SessionExtStore) GetSessionExt(sessionID string) *SessionExtModel {
	var m SessionExtModel
	result := s.db.Where("session_id = ?", sessionID).First(&m)
	if result.Error != nil {
		m = SessionExtModel{SessionID: sessionID}
	}
	return &m
}

// GetSessionExtBatch returns multiple session extension records in a single query.
func (s *SessionExtStore) GetSessionExtBatch(sessionIDs []string) map[string]*SessionExtModel {
	if len(sessionIDs) == 0 {
		return nil
	}
	var rows []SessionExtModel
	s.db.Where("session_id IN ?", sessionIDs).Find(&rows)
	m := make(map[string]*SessionExtModel, len(rows))
	for i := range rows {
		m[rows[i].SessionID] = &rows[i]
	}
	return m
}

// SaveSessionExt persists the session extension record (create or update).
// Todos 列由 todo 工具通过 SaveTodos 独立管理：调用方传入的记录可能是早于 SaveTodos
// 的过期快照（如 orchestrator 回合开始时的 ext），直接全字段 Save 会把最新待办清单覆盖为空。
// 因此保存前先合并数据库中已有的 Todos，保证会话待办不被误清。
func (s *SessionExtStore) SaveSessionExt(m *SessionExtModel) error {
	var existing SessionExtModel
	if err := s.db.Where("session_id = ?", m.SessionID).First(&existing).Error; err == nil {
		// 保留 todo 工具写入的最新待办，避免过期快照整行覆盖
		m.Todos = existing.Todos
	}
	return s.db.Save(m).Error
}

// GetApprovalMode returns the approval mode for a session. Returns "default" if not set.
func (s *SessionExtStore) GetApprovalMode(sessionID string) string {
	m := s.GetSessionExt(sessionID)
	if m.ApprovalMode != "" {
		return m.ApprovalMode
	}
	return "default"
}

// SetApprovalMode sets the approval mode for a session.
func (s *SessionExtStore) SetApprovalMode(sessionID, mode string) error {
	if mode != "default" && mode != "bypass" && mode != "auto" {
		mode = "default"
	}
	m := s.GetSessionExt(sessionID)
	m.ApprovalMode = mode
	return s.db.Save(m).Error
}

// GetTodos 返回会话的待办清单（无记录或为 nil 时返回空列表）。
func (s *SessionExtStore) GetTodos(sessionID string) (TodoList, error) {
	m := s.GetSessionExt(sessionID)
	if m.Todos == nil {
		return TodoList{}, nil
	}
	return m.Todos, nil
}

// SaveTodos 持久化会话的待办清单（全量替换语义）。
func (s *SessionExtStore) SaveTodos(sessionID string, todos TodoList) error {
	m := s.GetSessionExt(sessionID)
	m.Todos = todos
	return s.db.Save(m).Error
}

// Delete removes the session extension record for a session.
func (s *SessionExtStore) Delete(sessionID string) error {
	return s.db.Where("session_id = ?", sessionID).Delete(&SessionExtModel{}).Error
}
