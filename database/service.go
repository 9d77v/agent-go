// Package database provides shared database services for agent-go consumers:
//   - ADK session.Service (lazy initialized, separate adk_sessions.db)
//   - GORM-backed main database for common tables + business tables
//   - MessageMetadataStore / SessionExtStore (GORM-persisted)
//   - DB() accessor for business-layer queries and migrations
//
// Database layout:
//
//	<appDataDir>/data/
//	  ├── <dbFileName>        ← main DB (GORM): message_metadata, session_ext + business tables
//	  └── adk_sessions.db     ← ADK internal: sessions, events, app_states, user_states
package database

import (
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"google.golang.org/adk/v2/session"
	sessdb "google.golang.org/adk/v2/session/database"
	"google.golang.org/genai"
	"gorm.io/gorm"
)

// DBService provides ADK session.Service lazy initialization and
// a shared GORM database for common tables and business-layer extensions.
type DBService struct {
	dbPath     string
	db         *gorm.DB
	adkSvc     session.Service // lazy-init via GetSessionService()
	SessionExt *SessionExtStore
}

// NewDBService creates a new DBService.
// The main database is opened immediately and common tables are migrated.
func NewDBService(appDataDir, dbFileName string) *DBService {
	dbDir := filepath.Join(appDataDir, "data")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		log.Printf("[DB] failed to create data dir: %v", err)
	}

	dbPath := filepath.Join(dbDir, dbFileName)
	db, err := gorm.Open(sqlite.Open(dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"), &gorm.Config{})
	if err != nil {
		log.Printf("[DB] failed to open %s: %v", dbPath, err)
		return &DBService{dbPath: dbPath}
	}

	// Migrate framework common tables
	if err := MigrateCommon(db); err != nil {
		log.Printf("[DB] common migration failed: %v", err)
	}

	svc := &DBService{
		dbPath:     dbPath,
		db:         db,
		SessionExt: NewSessionExtStore(db),
	}

	// 预初始化 ADK session 数据库（创建 adk_sessions.db 并建表 + WAL checkpoint）
	// 避免后续查询报 "no such table"，同时确保 WAL 刷入主文件
	if _, err := svc.GetSessionService(); err != nil {
		log.Printf("[DB] ADK session DB init failed: %v", err)
	} else if adkDB, err := svc.GetADKMaintenanceDB(); err == nil {
		if err := Checkpoint(adkDB); err != nil {
			log.Printf("[DB] ADK WAL checkpoint 失败: %v", err)
		}
		adkDB.Close()
	}

	return svc
}

// DB returns the shared GORM database handle.
// Business layers use this for custom queries and AutoMigrate of business tables.
func (s *DBService) DB() *gorm.DB { return s.db }

// GetDBPath returns the database directory path.
func (s *DBService) GetDBPath() string { return filepath.Dir(s.dbPath) }

// GetSessionService returns the ADK session.Service (lazy initialized).
func (s *DBService) GetSessionService() (session.Service, error) {
	if s.adkSvc != nil {
		return s.adkSvc, nil
	}
	adkPath := filepath.Join(filepath.Dir(s.dbPath), "adk_sessions.db")
	svc, err := sessdb.NewSessionService(sqlite.Open(adkPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	if err := sessdb.AutoMigrate(svc); err != nil {
		return nil, err
	}
	s.adkSvc = svc
	return s.adkSvc, nil
}

// adkDB returns a read-only GORM connection to adk_sessions.db.
// 不缓存连接，每次查询都重新打开以确保看到最新的数据（读写连接可能已做了修改）。
func (s *DBService) adkDBConn() *gorm.DB {
	adkPath := filepath.Join(filepath.Dir(s.dbPath), "adk_sessions.db")
	db, err := gorm.Open(sqlite.Open(adkPath+"?mode=ro"), &gorm.Config{})
	if err != nil {
		return nil
	}
	return db
}

// GetADKMaintenanceDB opens a temporary read-write connection to adk_sessions.db
// for maintenance operations (WAL checkpoint, VACUUM). Caller must close the returned *sql.DB.
func (s *DBService) GetADKMaintenanceDB() (*sql.DB, error) {
	adkPath := filepath.Join(filepath.Dir(s.dbPath), "adk_sessions.db")
	db, err := gorm.Open(sqlite.Open(adkPath), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	return db.DB()
}

// DeleteADKEvents deletes all ADK events for a given session from adk_sessions.db.
func (s *DBService) DeleteADKEvents(sessionID string) error {
	adkPath := filepath.Join(filepath.Dir(s.dbPath), "adk_sessions.db")
	db, err := gorm.Open(sqlite.Open(adkPath), &gorm.Config{})
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	return db.Where("session_id = ?", sessionID).Delete(&adkEvent{}).Error
}

// ListADKSessions returns sessions filtered by app name from the ADK database.
func (s *DBService) ListADKSessions(appName string) ([]Session, error) {
	db := s.adkDBConn()
	if db == nil {
		return nil, nil
	}
	var rows []adkSession
	q := db.Order("create_time DESC").Limit(50)
	if appName != "" {
		q = q.Where("app_name = ?", appName)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	// 收集所有 session ID，批量查询第一条用户消息作为标题候选
	sidList := make([]string, len(rows))
	for i, r := range rows {
		sidList[i] = r.ID
	}
	firstMessages := s.getFirstUserMessages(sidList)

	result := make([]Session, len(rows))
	for i, r := range rows {
		title := r.ID // fallback
		if firstMsg, ok := firstMessages[r.ID]; ok && firstMsg != "" {
			if len([]rune(firstMsg)) > 50 {
				title = string([]rune(firstMsg)[:50]) + "..."
			} else {
				title = firstMsg
			}
		}
		result[i] = Session{
			SessionID: r.ID,
			Title:     title,
			CreatedAt: parseADKTime(r.CreatedAt),
			UpdatedAt: parseADKTime(r.UpdatedAt),
		}
	}
	return result, nil
}

// getFirstUserMessages queries the first user message content for each session.
func (s *DBService) getFirstUserMessages(sessionIDs []string) map[string]string {
	if len(sessionIDs) == 0 {
		return nil
	}
	db := s.adkDBConn()
	if db == nil {
		return nil
	}
	type eventRow struct {
		SessionID string `gorm:"column:session_id"`
		Content   []byte `gorm:"column:content"`
	}
	// SQLite: 取每个 session 的第一条 user 消息（按 rowid 排序）
	var rows []eventRow
	db.Raw(`
		SELECT e.session_id, e.content FROM events e
		INNER JOIN (
			SELECT session_id, MIN(rowid) as min_id FROM events
			WHERE session_id IN ? AND author = 'user'
			GROUP BY session_id
		) f ON e.session_id = f.session_id AND e.rowid = f.min_id
	`, sessionIDs).Scan(&rows)

	result := make(map[string]string, len(rows))
	for _, r := range rows {
		if len(r.Content) == 0 {
			continue
		}
		var c genai.Content
		if err := json.Unmarshal(r.Content, &c); err != nil {
			continue
		}
		var texts []string
		for _, part := range c.Parts {
			if part != nil && part.Text != "" {
				texts = append(texts, part.Text)
			}
		}
		if len(texts) > 0 {
			result[r.SessionID] = strings.Join(texts, "\n")
		}
	}
	return result
}

// GetADKSessionEvents returns messages/events for a session from the ADK database.
func (s *DBService) GetADKSessionEvents(sessionID string) ([]Message, error) {
	db := s.adkDBConn()
	if db == nil {
		return nil, nil
	}
	var rows []adkEvent
	if err := db.Where("session_id = ?", sessionID).Order("timestamp ASC").Find(&rows).Error; err != nil {
		return nil, err
	}

	// 第一遍：收集 FunctionResponse 结果（按 call ID/name 索引）
	toolResults := make(map[string]string) // name → result JSON
	for _, r := range rows {
		if len(r.Content) == 0 {
			continue
		}
		var c genai.Content
		if json.Unmarshal(r.Content, &c) != nil {
			continue
		}
		for _, part := range c.Parts {
			if part == nil || part.FunctionResponse == nil {
				continue
			}
			if respJSON, err := json.Marshal(part.FunctionResponse.Response); err == nil {
				name := part.FunctionResponse.Name
				if name != "" {
					toolResults[name] = string(respJSON)
				}
			}
		}
	}

	// 第二遍：构建消息列表
	// FunctionCall → role="model"，tool_call 含 id/name/args
	// FunctionResponse → role="tool"，tool_call 含 id/name/result（供前端第一遍收集 toolResults）
	result := make([]Message, 0, len(rows))
	for _, r := range rows {
		contentText := ""
		var reasoningText string
		var toolCalls []map[string]any
		hasResponse := false
		role := r.Author // 默认用 author

		if len(r.Content) > 0 {
			var c genai.Content
			if err := json.Unmarshal(r.Content, &c); err == nil {
				if c.Role != "" {
					role = c.Role // content 里的 role 是准确的
				}
				var texts []string
				for _, part := range c.Parts {
					if part == nil {
						continue
					}
					if part.Thought {
						if reasoningText != "" {
							reasoningText += "\n"
						}
						reasoningText += part.Text
					} else if part.Text != "" {
						texts = append(texts, part.Text)
					}
					if part.FunctionCall != nil {
						toolCalls = append(toolCalls, map[string]any{
							"id":   part.FunctionCall.ID,
							"name": part.FunctionCall.Name,
							"args": part.FunctionCall.Args,
						})
					}
					if part.FunctionResponse != nil {
						hasResponse = true
						tc := map[string]any{
							"id":   part.FunctionResponse.ID,
							"name": part.FunctionResponse.Name,
						}
						if respJSON, err := json.Marshal(part.FunctionResponse.Response); err == nil {
							tc["result"] = map[string]any{"output": string(respJSON)}
						}
						toolCalls = append(toolCalls, tc)
					}
				}
				contentText = strings.Join(texts, "\n")
			} else {
				contentText = string(r.Content)
			}
		}

		// FunctionResponse → role="tool"
		if hasResponse {
			role = "tool"
		}

		msg := Message{
			ID:        r.ID,
			SessionID: r.SessionID,
			Role:      role,
			Content:   contentText,
			Reasoning: reasoningText,
			CreatedAt: parseADKTime(r.Timestamp),
		}

		if len(toolCalls) > 0 {
			tcJSON, _ := json.Marshal(toolCalls)
			msg.ToolCall = tcJSON
		}

		result = append(result, msg)
	}
	return result, nil
}

func parseADKTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
