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
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/session"
	sessdb "google.golang.org/adk/v2/session/database"
	"google.golang.org/genai"
	"gorm.io/gorm"
)

// DBService provides ADK session.Service lazy initialization and
// a shared GORM database for common tables and business-layer extensions.
type DBService struct {
	dbPath      string
	db          *gorm.DB
	adkSvc      session.Service  // lazy-init via GetSessionService()
	artifactSvc artifact.Service // NewDBService 启动时初始化
	SessionExt  *SessionExtStore
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
		dbPath:      dbPath,
		db:          db,
		SessionExt:  NewSessionExtStore(db),
		artifactSvc: newSQLiteArtifactService(db),
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

// GetArtifactService returns the SQLite-backed artifact.Service.
// 制品服务在 NewDBService 启动时统一初始化（blob_files / artifacts 表随 CommonModels 迁移），此处仅为访问器。
func (s *DBService) GetArtifactService() artifact.Service {
	return s.artifactSvc
}

// FindBlobBySHA256 按内容 sha256 查找文件实体（去重）。
func (s *DBService) FindBlobBySHA256(sha256 string) (*BlobFile, bool, error) {
	if s.db == nil || sha256 == "" {
		return nil, false, nil
	}
	var b BlobFile
	err := s.db.Where("sha256 = ?", sha256).First(&b).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &b, true, nil
}

// UpsertBlobFile 插入或更新文件实体（sha256 唯一，幂等）。
func (s *DBService) UpsertBlobFile(sha256, remoteURL, mimeType string, size int64) error {
	if s.db == nil || sha256 == "" {
		return nil
	}
	var b BlobFile
	err := s.db.Where("sha256 = ?", sha256).First(&b).Error
	if err == nil {
		b.RemoteURL = remoteURL
		b.MimeType = mimeType
		b.Size = size
		return s.db.Save(&b).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.db.Create(&BlobFile{ID: newUUID(), SHA256: sha256, RemoteURL: remoteURL, MimeType: mimeType, Size: size}).Error
	}
	return err
}

// FindArtifactByBlobSHA256 按文件 sha256 查找会话内已有 artifact（去重复用文件名）。
func (s *DBService) FindArtifactByBlobSHA256(appName, userID, sessionID, blobSHA string) (string, bool, error) {
	svc := s.GetArtifactService()
	if sa, ok := svc.(*sqliteArtifactService); ok {
		return sa.FindArtifactByBlobSHA256(appName, userID, sessionID, blobSHA)
	}
	return "", false, nil
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

// RepairDanglingSession 修复会话末尾可能存在的悬空 tool call。
// 典型场景：达到最大迭代次数中断时，ADK 已把含 FunctionCall 的模型响应写入会话，
// 但对应的 FunctionResponse 尚未产出，导致后续继续执行（continue 或发新消息）时
// Responses API 报 "No tool output found for tool call ..."。
// 策略：回滚到最后一个"所有 tool call 均已补齐"的安全点，删除其后所有事件。
// 返回删除的事件数；会话完整或无法定位安全点时返回 0（不删任何数据）。
func (s *DBService) RepairDanglingSession(sessionID string) (int, error) {
	adkPath := filepath.Join(filepath.Dir(s.dbPath), "adk_sessions.db")

	type eventRow struct {
		ID      string `gorm:"column:id"`
		Content []byte `gorm:"column:content"`
		RowID   int64  `gorm:"column:rowid"`
	}

	ro := s.adkDBConn()
	if ro == nil {
		return 0, nil
	}
	var rows []eventRow
	if err := ro.Raw(
		"SELECT id, content, rowid FROM events WHERE session_id = ? ORDER BY rowid ASC", sessionID,
	).Scan(&rows).Error; err != nil {
		_ = closeDB(ro)
		return 0, err
	}
	_ = closeDB(ro)

	if len(rows) == 0 {
		return 0, nil
	}

	pending := make(map[string]bool) // 尚未获得对应工具输出的 tool call ID
	var lastSafeRowID int64
	for _, r := range rows {
		if len(r.Content) == 0 {
			continue
		}
		var c genai.Content
		if json.Unmarshal(r.Content, &c) != nil {
			continue
		}
		for _, part := range c.Parts {
			if part == nil {
				continue
			}
			if part.FunctionCall != nil && part.FunctionCall.ID != "" {
				pending[part.FunctionCall.ID] = true
			}
			if part.FunctionResponse != nil && part.FunctionResponse.ID != "" {
				delete(pending, part.FunctionResponse.ID)
			}
		}
		// 处理完该事件后若所有调用均已补齐，则记录为安全点
		if len(pending) == 0 {
			lastSafeRowID = r.RowID
		}
	}

	if len(pending) == 0 || lastSafeRowID == 0 {
		// 会话完整，或无法定位安全点（保守起见不删，避免清空会话）
		return 0, nil
	}

	rw, err := gorm.Open(sqlite.Open(adkPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"), &gorm.Config{})
	if err != nil {
		return 0, err
	}
	defer closeDB(rw)

	res := rw.Where("session_id = ? AND rowid > ?", sessionID, lastSafeRowID).Delete(&adkEvent{})
	if res.Error != nil {
		return 0, res.Error
	}
	return int(res.RowsAffected), nil
}

// closeDB closes the underlying *sql.DB of a *gorm.DB, ignoring errors.
func closeDB(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
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
			firstMsg = maskArtifactPlaceholders(firstMsg)
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

// artifactPlaceholderRE 匹配 ADK 对输入 blob（图片等）生成的制品占位符。
var artifactPlaceholderRE = regexp.MustCompile(`Uploaded file: artifact_[^\s]+\. It has been saved to the artifacts`)

// maskArtifactPlaceholders 将制品占位符替换为图片标记，用于会话标题/消息展示。
func maskArtifactPlaceholders(s string) string {
	return artifactPlaceholderRE.ReplaceAllString(s, "🖼 [图片]")
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
