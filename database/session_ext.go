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
func (s *SessionExtStore) SaveSessionExt(m *SessionExtModel) error {
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

// Delete removes the session extension record for a session.
func (s *SessionExtStore) Delete(sessionID string) error {
	return s.db.Where("session_id = ?", sessionID).Delete(&SessionExtModel{}).Error
}
