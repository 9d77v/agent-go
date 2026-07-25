package msgdb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Migration 自定义迁移
type Migration struct {
	Version int
	SQL     string
}

// Store 数据库管理器
type Store struct {
	db         *sql.DB
	dbPath     string
	migrations []Migration
}

// NewStore 创建数据库管理器
func NewStore(dbPath string) *Store {
	return &Store{dbPath: dbPath}
}

// RegisterMigration 注册自定义迁移，Open 前调用
func (s *Store) RegisterMigration(m Migration) {
	s.migrations = append(s.migrations, m)
}

// Open 打开数据库连接，执行迁移
func (s *Store) Open() error {
	var err error
	s.db, err = sql.Open("sqlite", s.dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("打开数据库失败: %w", err)
	}
	s.db.SetMaxOpenConns(1)
	s.db.SetMaxIdleConns(1)

	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS _migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("创建迁移表失败: %w", err)
	}

	core := []Migration{
		{Version: 1, SQL: `
			CREATE TABLE IF NOT EXISTS messages (
				id TEXT PRIMARY KEY,
				session_id TEXT NOT NULL DEFAULT '',
				seq INTEGER NOT NULL DEFAULT 0,
				turn_id TEXT NOT NULL DEFAULT '',
				client_msg_id TEXT NOT NULL DEFAULT '',
				role TEXT NOT NULL DEFAULT '',
				content TEXT NOT NULL DEFAULT '',
				reasoning TEXT NOT NULL DEFAULT '',
				tool_call JSON NOT NULL DEFAULT '[]',
				metadata TEXT NOT NULL DEFAULT '{}',
				created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
			);
			CREATE TABLE IF NOT EXISTS sessions (
				session_id TEXT NOT NULL PRIMARY KEY,
				title TEXT NOT NULL DEFAULT '新会话',
				workspace_id TEXT NOT NULL DEFAULT '',
				approval_mode TEXT NOT NULL DEFAULT 'default',
				token_usage JSON NOT NULL DEFAULT '{}',
				llm_last_sent_msg_id TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL DEFAULT (datetime('now','localtime')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
			);
			CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id);
			CREATE INDEX IF NOT EXISTS idx_messages_session_seq ON messages(session_id, seq);
			CREATE INDEX IF NOT EXISTS idx_sessions_workspace_id ON sessions(workspace_id);
		`},
	}

	all := append(core, s.migrations...)
	for _, m := range all {
		var cnt int
		s.db.QueryRow(`SELECT COUNT(*) FROM _migrations WHERE version=?`, m.Version).Scan(&cnt)
		if cnt > 0 {
			continue
		}
		if _, err := s.db.Exec(m.SQL); err != nil {
			return fmt.Errorf("迁移 v%d 失败: %w", m.Version, err)
		}
		s.db.Exec(`INSERT INTO _migrations (version, applied_at) VALUES (?, datetime('now','localtime'))`, m.Version)
		log.Printf("[DB] 迁移 v%d 已应用", m.Version)
	}

	log.Printf("[DB] 数据库已就绪: %s", s.dbPath)
	return nil
}

// Close 关闭数据库
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// GetDBPath 返回数据库文件路径
func (s *Store) GetDBPath() string {
	return s.dbPath
}

// DB 返回底层 *sql.DB
func (s *Store) DB() *sql.DB {
	return s.db
}

// Checkpoint 手动 WAL checkpoint
func (s *Store) Checkpoint() error {
	_, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

// Vacuum 回收空白空间
func (s *Store) Vacuum() error {
	_, err := s.db.Exec("VACUUM")
	return err
}

// InsertMessage 插入或替换消息，自动分配 seq
func (s *Store) InsertMessage(msg *Message) error {
	if msg.ID == "" {
		msg.ID = uuid.Must(uuid.NewV7()).String()
	}
	msg.CreatedAt = time.Now()
	if msg.Seq == 0 {
		var maxSeq int64
		s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE session_id=?`, msg.SessionID).Scan(&maxSeq)
		msg.Seq = maxSeq + 1
	}
	meta := msg.Metadata
	if len(meta) == 0 {
		meta = json.RawMessage("{}")
	}
	tc := msg.ToolCall
	if len(tc) == 0 {
		tc = json.RawMessage("[]")
	}
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO messages (id, session_id, seq, turn_id, client_msg_id, role, content, reasoning, tool_call, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.SessionID, msg.Seq, msg.TurnID, msg.ClientMsgID,
		msg.Role, msg.Content, msg.Reasoning, string(tc), string(meta),
		msg.CreatedAt.Format("2006-01-02 15:04:05"),
	)
	return err
}

// ListMessages 列出会话全部消息
func (s *Store) ListMessages(sessionID string) ([]Message, error) {
	rows, err := s.db.Query(
		`SELECT id, session_id, seq, turn_id, client_msg_id, role, content, reasoning, tool_call, metadata, created_at
		 FROM messages WHERE session_id=? ORDER BY seq ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []Message
	for rows.Next() {
		var m Message
		var createdAt, tcStr, metaStr string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Seq, &m.TurnID, &m.ClientMsgID,
			&m.Role, &m.Content, &m.Reasoning, &tcStr, &metaStr, &createdAt); err != nil {
			return nil, err
		}
		m.ToolCall = json.RawMessage(tcStr)
		m.Metadata = json.RawMessage(metaStr)
		m.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		msgs = append(msgs, m)
	}
	return msgs, nil
}

// UpdateMessageMetadata 更新消息 metadata
func (s *Store) UpdateMessageMetadata(messageID, metadata string) error {
	_, err := s.db.Exec(`UPDATE messages SET metadata=? WHERE id=?`, metadata, messageID)
	return err
}

// MarkRetryDone 原子化标记消息已重试（使用 SQL json_set 避免竞态）
func (s *Store) MarkRetryDone(messageID string) error {
	if messageID == "" {
		return nil
	}
	_, err := s.db.Exec(
		`UPDATE messages SET metadata = json_set(COALESCE(NULLIF(metadata,''), '{}'), '$.retry_done', json('true')) WHERE id=?`,
		messageID,
	)
	return err
}

// CreateSession 创建会话
func (s *Store) CreateSession(sessionID, workspaceID string) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO sessions (session_id, title, workspace_id, approval_mode, created_at, updated_at)
		 VALUES (?, '新会话', ?, 'default', datetime('now','localtime'), datetime('now','localtime'))`,
		sessionID, workspaceID)
	return err
}

// GetApprovalMode 获取会话审批模式
func (s *Store) GetApprovalMode(sessionID string) string {
	var mode string
	err := s.db.QueryRow(`SELECT approval_mode FROM sessions WHERE session_id=?`, sessionID).Scan(&mode)
	if err != nil {
		return "default"
	}
	if mode != "bypass" && mode != "auto" {
		mode = "default"
	}
	return mode
}

// SetApprovalMode 设置会话审批模式
func (s *Store) SetApprovalMode(sessionID, mode string) error {
	if mode != "default" && mode != "bypass" && mode != "auto" {
		mode = "default"
	}
	_, err := s.db.Exec(`UPDATE sessions SET approval_mode=?, updated_at=datetime('now','localtime') WHERE session_id=?`, mode, sessionID)
	return err
}

// UpdateSessionTitle 更新会话标题
func (s *Store) UpdateSessionTitle(sessionID, title string) error {
	_, err := s.db.Exec(`UPDATE sessions SET title=?, updated_at=datetime('now','localtime') WHERE session_id=?`, title, sessionID)
	return err
}

// UpdateSessionTokens 更新 token 用量
func (s *Store) UpdateSessionTokens(sessionID string, prompt, completion, total int) error {
	data, _ := json.Marshal(map[string]int{
		"prompt_tokens": prompt, "completion_tokens": completion, "total_tokens": total,
	})
	_, err := s.db.Exec(`UPDATE sessions SET token_usage=?, updated_at=datetime('now','localtime') WHERE session_id=?`, string(data), sessionID)
	return err
}

// UpdateLLMLastSentMsgID 记录最近发送给 LLM 的消息 ID
func (s *Store) UpdateLLMLastSentMsgID(sessionID, msgID string) error {
	_, err := s.db.Exec(`UPDATE sessions SET llm_last_sent_msg_id=?, updated_at=datetime('now','localtime') WHERE session_id=?`, msgID, sessionID)
	return err
}

// ListSessions 列出会话
func (s *Store) ListSessions(limit int, workspaceID ...string) ([]Session, error) {
	var rows *sql.Rows
	var err error
	if len(workspaceID) > 0 && workspaceID[0] != "" {
		rows, err = s.db.Query(
			`SELECT session_id, title, created_at, updated_at, COALESCE(token_usage,''), workspace_id
			 FROM sessions WHERE workspace_id=? ORDER BY updated_at DESC LIMIT ?`, workspaceID[0], limit)
	} else {
		rows, err = s.db.Query(
			`SELECT session_id, title, created_at, updated_at, COALESCE(token_usage,''), workspace_id
			 FROM sessions ORDER BY updated_at DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []Session
	for rows.Next() {
		var s Session
		var createdAtStr, updatedAtStr string
		if err := rows.Scan(&s.SessionID, &s.Title, &createdAtStr, &updatedAtStr, &s.TokenUsage, &s.WorkspaceID); err != nil {
			return nil, err
		}
		s.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAtStr)
		s.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAtStr)
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// GetSessionTokens 读取会话 token 用量
func (s *Store) GetSessionTokens(sessionID string) (prompt, completion, total int) {
	var raw string
	err := s.db.QueryRow(`SELECT COALESCE(token_usage, '{}') FROM sessions WHERE session_id=?`, sessionID).Scan(&raw)
	if err != nil || raw == "" || raw == "{}" {
		return 0, 0, 0
	}
	var info struct {
		Prompt     int `json:"prompt_tokens"`
		Completion int `json:"completion_tokens"`
		Total      int `json:"total_tokens"`
	}
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		return 0, 0, 0
	}
	return info.Prompt, info.Completion, info.Total
}

// GetSession 获取单个会话
func (s *Store) GetSession(sessionID string) (*Session, error) {
	var sess Session
	var createdAtStr, updatedAtStr string
	err := s.db.QueryRow(
		`SELECT session_id, title, created_at, updated_at, COALESCE(token_usage,''), workspace_id
		 FROM sessions WHERE session_id=?`, sessionID,
	).Scan(&sess.SessionID, &sess.Title, &createdAtStr, &updatedAtStr, &sess.TokenUsage, &sess.WorkspaceID)
	if err != nil {
		return nil, err
	}
	sess.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAtStr)
	sess.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAtStr)
	return &sess, nil
}

// DeleteSession 删除会话及所有消息
func (s *Store) DeleteSession(sessionID string) error {
	if _, err := s.db.Exec(`DELETE FROM messages WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM sessions WHERE session_id=?`, sessionID)
	return err
}
