// Package memory 提供基于 SQLite 的 ADK memory.Service 实现。
package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// Service 是 memory 服务的接口，与 ADK-Go memory.Service 兼容。
type Service interface {
	AddSessionToMemory(ctx context.Context, s session.Session) error
	SearchMemory(ctx context.Context, req *SearchRequest) (*SearchResponse, error)
}

// SearchRequest 记忆搜索请求。
type SearchRequest struct {
	Query   string
	UserID  string
	AppName string
}

// SearchResponse 记忆搜索响应。
type SearchResponse struct {
	Memories []Entry
}

// Entry 单条记忆。
type Entry struct {
	ID             string
	Content        *genai.Content
	Author         string
	Timestamp      time.Time
	CustomMetadata map[string]any
}

// SQLiteService 基于 SQLite 的持久化 memory.Service。
type SQLiteService struct {
	db *sql.DB
	mu sync.RWMutex
}

// NewSQLiteService 创建 SQLite memory 服务。
// db 必须是已打开的 *sql.DB 连接。
func NewSQLiteService(db *sql.DB) (*SQLiteService, error) {
	s := &SQLiteService{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *SQLiteService) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS memory_entries (
			id TEXT NOT NULL,
			app_name TEXT NOT NULL,
			user_id TEXT NOT NULL,
			content_json BLOB,
			words_json BLOB,
			author TEXT,
			timestamp DATETIME,
			custom_metadata BLOB,
			PRIMARY KEY (id, app_name, user_id)
		);
		CREATE INDEX IF NOT EXISTS idx_memory_app_user ON memory_entries(app_name, user_id);
	`)
	return err
}

// AddSessionToMemory 从 session 的 LLM 响应中提取记忆并存储。
func (s *SQLiteService) AddSessionToMemory(ctx context.Context, sess session.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	appName := sess.AppName()
	userID := sess.UserID()

	for event := range sess.Events().All() {
		if event.Content == nil {
			continue
		}
		content := event.Content
		texts := extractTexts(content)
		if len(texts) == 0 {
			continue
		}
		fullText := strings.Join(texts, " ")
		words := extractWords(fullText)
		if len(words) == 0 {
			continue
		}

		contentJSON, _ := json.Marshal(content)
		wordsJSON, _ := json.Marshal(wordsToSlice(words))
		metaJSON, _ := json.Marshal(event.CustomMetadata)

		_, err := s.db.ExecContext(ctx,
			`INSERT OR REPLACE INTO memory_entries (id, app_name, user_id, content_json, words_json, author, timestamp, custom_metadata)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			event.ID, appName, userID, contentJSON, wordsJSON, event.Author, event.Timestamp, metaJSON,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

// SearchMemory 搜索与查询匹配的记忆。
func (s *SQLiteService) SearchMemory(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	queryWords := extractWords(req.Query)
	if len(queryWords) == 0 {
		return &SearchResponse{}, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, content_json, author, timestamp, custom_metadata, words_json
		 FROM memory_entries WHERE app_name = ? AND user_id = ?`,
		req.AppName, req.UserID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	resp := &SearchResponse{}
	for rows.Next() {
		var id string
		var contentJSON, wordsJSON []byte
		var author string
		var timestamp time.Time
		var metaJSON []byte

		if err := rows.Scan(&id, &contentJSON, &author, &timestamp, &metaJSON, &wordsJSON); err != nil {
			continue
		}

		var storedWords []string
		json.Unmarshal(wordsJSON, &storedWords)
		wordSet := sliceToWords(storedWords)

		if !anyWordMatch(queryWords, wordSet) {
			continue
		}

		var content genai.Content
		json.Unmarshal(contentJSON, &content)
		var meta map[string]any
		json.Unmarshal(metaJSON, &meta)

		resp.Memories = append(resp.Memories, Entry{
			ID:             id,
			Content:        &content,
			Author:         author,
			Timestamp:      timestamp,
			CustomMetadata: meta,
		})
	}
	return resp, nil
}

// ---------- helpers ----------

func extractTexts(content *genai.Content) []string {
	var texts []string
	for _, part := range content.Parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return texts
}

func extractWords(text string) map[string]struct{} {
	words := make(map[string]struct{})
	for w := range strings.FieldsSeq(strings.ToLower(text)) {
		w = strings.Trim(w, ".,!?;:()[]{}\"'")
		if len(w) > 1 {
			words[w] = struct{}{}
		}
	}
	return words
}

func wordsToSlice(wm map[string]struct{}) []string {
	s := make([]string, 0, len(wm))
	for w := range wm {
		s = append(s, w)
	}
	return s
}

func sliceToWords(s []string) map[string]struct{} {
	wm := make(map[string]struct{}, len(s))
	for _, w := range s {
		wm[w] = struct{}{}
	}
	return wm
}

func anyWordMatch(query, stored map[string]struct{}) bool {
	if len(query) < len(stored) {
		for w := range query {
			if _, ok := stored[w]; ok {
				return true
			}
		}
	} else {
		for w := range stored {
			if _, ok := query[w]; ok {
				return true
			}
		}
	}
	return false
}
