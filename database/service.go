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
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/session"
	"gorm.io/gorm"
)

// DBService provides ADK session.Service lazy initialization and
// a shared GORM database for common tables and business-layer extensions.
type DBService struct {
	dbPath      string
	db          *gorm.DB
	adk         *ADKStore        // adk_sessions.db 访问（会话服务/事件/维护）
	artifactSvc artifact.Service // NewDBService 启动时初始化
	SessionExt  *SessionExtStore
}

// NewDBService creates a new DBService.
// The main database is opened immediately and common tables are migrated.
func NewDBService(appDataDir, dbFileName string) (*DBService, error) {
	dbDir := filepath.Join(appDataDir, "data")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	dbPath := filepath.Join(dbDir, dbFileName)
	db, err := gorm.Open(sqlite.Open(dbPath+"?"+sqliteWALParams), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open db %s: %w", dbPath, err)
	}

	// Migrate framework common tables
	if err := MigrateCommon(db); err != nil {
		return nil, fmt.Errorf("migrate common tables: %w", err)
	}

	svc := &DBService{
		dbPath:      dbPath,
		db:          db,
		adk:         newADKStore(dbDir),
		SessionExt:  NewSessionExtStore(db),
		artifactSvc: newSQLiteArtifactService(db),
	}

	// 预初始化 ADK session 数据库（创建 adk_sessions.db 并建表 + WAL checkpoint）
	// 避免后续查询报 "no such table"，同时确保 WAL 刷入主文件。
	// 失败不致命：GetSessionService 懒加载会重试。
	if _, err := svc.GetSessionService(); err != nil {
		log.Printf("[DB] ADK session DB init failed: %v", err)
	} else if adkDB, err := svc.GetADKMaintenanceDB(); err == nil {
		if err := Checkpoint(adkDB); err != nil {
			log.Printf("[DB] ADK WAL checkpoint 失败: %v", err)
		}
		adkDB.Close()
	}

	return svc, nil
}

// DB returns the shared GORM database handle.
// Business layers use this for custom queries and AutoMigrate of business tables.
func (s *DBService) DB() *gorm.DB { return s.db }

// GetDBPath returns the database directory path.
func (s *DBService) GetDBPath() string { return filepath.Dir(s.dbPath) }

// GetSessionService returns the ADK session.Service (lazy initialized).
func (s *DBService) GetSessionService() (session.Service, error) {
	return s.adk.Service()
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

// adkDBConn returns a read-only GORM connection to adk_sessions.db.
func (s *DBService) adkDBConn() *gorm.DB {
	return s.adk.readConn()
}

// GetADKMaintenanceDB opens a temporary read-write connection to adk_sessions.db
// for maintenance operations (WAL checkpoint, VACUUM). Caller must close the returned *sql.DB.
func (s *DBService) GetADKMaintenanceDB() (*sql.DB, error) {
	return s.adk.MaintenanceDB()
}

// DeleteADKEvents deletes all ADK events for a given session from adk_sessions.db.
func (s *DBService) DeleteADKEvents(sessionID string) error {
	return s.adk.DeleteEvents(sessionID)
}

// RepairDanglingSession 修复会话末尾可能存在的悬空 tool call。
// 委托 ADKStore.RepairDanglingSession。
func (s *DBService) RepairDanglingSession(sessionID string) (int, error) {
	return s.adk.RepairDanglingSession(sessionID)
}

// ListADKSessions returns sessions filtered by app name from the ADK database.
func (s *DBService) ListADKSessions(appName string) ([]Session, error) {
	return s.adk.ListSessions(appName)
}

// GetADKSessionEvents returns messages/events for a session from the ADK database.
func (s *DBService) GetADKSessionEvents(sessionID string) ([]Message, error) {
	return s.adk.SessionEvents(sessionID)
}
