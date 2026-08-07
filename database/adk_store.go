package database

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"google.golang.org/adk/v2/session"
	sessdb "google.golang.org/adk/v2/session/database"
	adktoolconfirmation "google.golang.org/adk/v2/tool/toolconfirmation"
	"google.golang.org/genai"
	"gorm.io/gorm"
)

// ADKStore 封装 adk_sessions.db 的访问：会话服务懒加载、事件读取、
// 悬空会话修复与维护连接。与主库（GORM business tables）职责分离。
type ADKStore struct {
	adkPath string
	adkSvc  session.Service // lazy-init via Service()
}

// newADKStore creates an ADKStore rooted at the given data directory.
func newADKStore(dbDir string) *ADKStore {
	return &ADKStore{adkPath: filepath.Join(dbDir, "adk_sessions.db")}
}

// Service returns the ADK session.Service (lazy initialized).
func (a *ADKStore) Service() (session.Service, error) {
	if a.adkSvc != nil {
		return a.adkSvc, nil
	}
	svc, err := sessdb.NewSessionService(sqlite.Open(a.adkPath+"?"+sqliteWALParams), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	if err := sessdb.AutoMigrate(svc); err != nil {
		return nil, err
	}
	a.adkSvc = svc
	return a.adkSvc, nil
}

// readConn opens a read-only GORM connection to adk_sessions.db.
// 不缓存连接，每次查询都重新打开以确保看到最新的数据（读写连接可能已做了修改）。
func (a *ADKStore) readConn() *gorm.DB {
	db, err := gorm.Open(sqlite.Open(a.adkPath+"?"+sqliteReadOnlyMode), &gorm.Config{})
	if err != nil {
		return nil
	}
	return db
}

// MaintenanceDB opens a temporary read-write connection to adk_sessions.db
// for maintenance operations (WAL checkpoint, VACUUM). Caller must close the returned *sql.DB.
func (a *ADKStore) MaintenanceDB() (*sql.DB, error) {
	db, err := gorm.Open(sqlite.Open(a.adkPath), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	return db.DB()
}

// DeleteEvents deletes all ADK events for a given session from adk_sessions.db.
func (a *ADKStore) DeleteEvents(sessionID string) error {
	db, err := gorm.Open(sqlite.Open(a.adkPath), &gorm.Config{})
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
func (a *ADKStore) RepairDanglingSession(sessionID string) (int, error) {
	type eventRow struct {
		ID      string `gorm:"column:id"`
		Content []byte `gorm:"column:content"`
		RowID   int64  `gorm:"column:rowid"`
	}

	ro := a.readConn()
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
				// 确认事件（adk_request_confirmation）等待用户恢复，不是悬空调用。
				// 若计入 pending，审批恢复前的 RepairDanglingSession 会把它误删，
				// 导致 RequestConfirmationRequestProcessor 找不到确认事件、工具无法重放
				//（模型只看到占位结果 "requires confirmation"，误以为审批还在等待）。
				if part.FunctionCall.Name == adktoolconfirmation.FunctionCallName {
					continue
				}
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

	rw, err := gorm.Open(sqlite.Open(a.adkPath+"?"+sqliteWALParams), &gorm.Config{})
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

// ListSessions returns sessions filtered by app name from the ADK database.
func (a *ADKStore) ListSessions(appName string) ([]Session, error) {
	db := a.readConn()
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
	firstMessages := a.firstUserMessages(sidList)

	result := make([]Session, len(rows))
	for i, r := range rows {
		title := r.ID // fallback
		if firstMsg, ok := firstMessages[r.ID]; ok && firstMsg != "" {
			firstMsg = maskArtifactPlaceholders(firstMsg)
			if len([]rune(firstMsg)) > maxTitleRunes {
				title = string([]rune(firstMsg)[:maxTitleRunes]) + "..."
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

// SessionEvents returns messages/events for a session from the ADK database.
func (a *ADKStore) SessionEvents(sessionID string) ([]Message, error) {
	db := a.readConn()
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

// firstUserMessages queries the first user message content for each session.
func (a *ADKStore) firstUserMessages(sessionIDs []string) map[string]string {
	if len(sessionIDs) == 0 {
		return nil
	}
	db := a.readConn()
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

// artifactPlaceholderRE 匹配 ADK 对输入 blob（图片等）生成的制品占位符。
var artifactPlaceholderRE = regexp.MustCompile(`Uploaded file: artifact_[^\s]+\. It has been saved to the artifacts`)

// maskArtifactPlaceholders 将制品占位符替换为图片标记，用于会话标题/消息展示。
func maskArtifactPlaceholders(s string) string {
	return artifactPlaceholderRE.ReplaceAllString(s, "🖼 [图片]")
}

// parseADKTime parses an ADK timestamp string.
func parseADKTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
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
