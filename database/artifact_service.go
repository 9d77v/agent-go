package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/genai"
	"gorm.io/gorm"
)

// BlobFile 已上传的文件实体（按内容 sha256 全局唯一）。
// 文件只与内容相关，与会话无关：同一文件（同 sha256）只存一条、远程 URL 唯一，不重复上传 OSS。
type BlobFile struct {
	ID        string `gorm:"primaryKey;size:36"` // UUID v7
	SHA256    string `gorm:"uniqueIndex"`        // 图片内容 sha256（唯一）
	RemoteURL string `gorm:"index"`              // OSS 远程地址
	MimeType  string
	Size      int64
	CreatedAt time.Time
}

// Artifact 会话内的 artifact 记录（ADK artifact 语义），通过 BlobSHA256 关联 BlobFile。
// artifact 与会话（session_id）挂钩；文件属性（sha256/URL）在 BlobFile，二者分离。
type Artifact struct {
	ID         string `gorm:"primaryKey;size:36"` // UUID v7
	AppName    string `gorm:"index:idx_artifact_scope"`
	UserID     string `gorm:"index:idx_artifact_scope"`
	SessionID  string `gorm:"index:idx_artifact_scope"`
	FileName   string `gorm:"index:idx_artifact_scope"`
	Version    int64  `gorm:"index:idx_artifact_version"`
	BlobSHA256 string `gorm:"index:idx_artifact_blob"` // 关联 BlobFile.SHA256
	PartJSON   string `gorm:"type:text"`
	CreatedAt  time.Time
}

// newUUID 生成 UUID v7 字符串主键。
func newUUID() string {
	return uuid.Must(uuid.NewV7()).String()
}

// ExtractSHA256FromURL 从 OSS 图片 URL 提取内容 sha256（路径最后一段 <64位sha256>.webp）。
// 非标准 URL 返回空串（调用方安全降级：不去重）。
func ExtractSHA256FromURL(u string) string {
	trimmed := strings.TrimRight(u, "/")
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		seg := strings.TrimSuffix(trimmed[idx+1:], ".webp")
		if len(seg) == 64 && !strings.ContainsAny(seg, "/.?") {
			return seg
		}
	}
	return ""
}

// sqliteArtifactService artifact.Service 的 SQLite 实现。
// 制品以 JSON 化的 genai.Part 存储，支持版本管理与跨重启持久化。
type sqliteArtifactService struct {
	db *gorm.DB
}

// newSQLiteArtifactService 创建 SQLite 制品服务。
func newSQLiteArtifactService(db *gorm.DB) artifact.Service {
	return &sqliteArtifactService{db: db}
}

// scopeSession user: 前缀的制品归入用户命名空间（跨会话可见），否则限定在会话内。
func scopeSession(sessionID, fileName string) string {
	if strings.HasPrefix(fileName, "user:") {
		return "user"
	}
	return sessionID
}

// Save 保存制品，返回版本号。Part.Text 为图片 URL 时记录 BlobSHA256 关联文件实体。
func (s *sqliteArtifactService) Save(ctx context.Context, req *artifact.SaveRequest) (*artifact.SaveResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid save request: %w", err)
	}
	partJSON, err := json.Marshal(req.Part)
	if err != nil {
		return nil, fmt.Errorf("marshal artifact part: %w", err)
	}
	sess := scopeSession(req.SessionID, req.FileName)

	version := req.Version
	if version == 0 {
		var maxV int64
		if err := s.db.Model(&Artifact{}).
			Where("app_name = ? AND user_id = ? AND session_id = ? AND file_name = ?",
				req.AppName, req.UserID, sess, req.FileName).
			Select("COALESCE(MAX(version), 0)").Scan(&maxV).Error; err != nil {
			return nil, err
		}
		version = maxV + 1
	}

	blobSHA := ""
	if req.Part != nil && req.Part.Text != "" {
		blobSHA = ExtractSHA256FromURL(req.Part.Text)
	}

	row := Artifact{
		ID:         newUUID(),
		AppName:    req.AppName,
		UserID:     req.UserID,
		SessionID:  sess,
		FileName:   req.FileName,
		Version:    version,
		BlobSHA256: blobSHA,
		PartJSON:   string(partJSON),
		CreatedAt:  time.Now(),
	}
	if err := s.db.Create(&row).Error; err != nil {
		return nil, err
	}
	return &artifact.SaveResponse{Version: version}, nil
}

// Load 加载制品（未指定版本时取最新）。
func (s *sqliteArtifactService) Load(ctx context.Context, req *artifact.LoadRequest) (*artifact.LoadResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid load request: %w", err)
	}
	sess := scopeSession(req.SessionID, req.FileName)

	q := s.db.Where("app_name = ? AND user_id = ? AND session_id = ? AND file_name = ?",
		req.AppName, req.UserID, sess, req.FileName)
	if req.Version != 0 {
		q = q.Where("version = ?", req.Version)
	}
	var row Artifact
	if err := q.Order("version DESC").First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("artifact %s not found", req.FileName)
		}
		return nil, err
	}
	var part genai.Part
	if err := json.Unmarshal([]byte(row.PartJSON), &part); err != nil {
		return nil, fmt.Errorf("unmarshal artifact part: %w", err)
	}
	return &artifact.LoadResponse{Part: &part}, nil
}

// Delete 删除制品（未指定版本时删除全部版本）。
func (s *sqliteArtifactService) Delete(ctx context.Context, req *artifact.DeleteRequest) error {
	if err := req.Validate(); err != nil {
		return fmt.Errorf("invalid delete request: %w", err)
	}
	sess := scopeSession(req.SessionID, req.FileName)

	q := s.db.Where("app_name = ? AND user_id = ? AND session_id = ? AND file_name = ?",
		req.AppName, req.UserID, sess, req.FileName)
	if req.Version != 0 {
		q = q.Where("version = ?", req.Version)
	}
	return q.Delete(&Artifact{}).Error
}

// List 列出会话内所有制品文件名。
func (s *sqliteArtifactService) List(ctx context.Context, req *artifact.ListRequest) (*artifact.ListResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid list request: %w", err)
	}
	var names []string
	if err := s.db.Model(&Artifact{}).
		Where("app_name = ? AND user_id = ? AND session_id = ?", req.AppName, req.UserID, req.SessionID).
		Distinct().Order("file_name ASC").Pluck("file_name", &names).Error; err != nil {
		return nil, err
	}
	if names == nil {
		names = []string{}
	}
	return &artifact.ListResponse{FileNames: names}, nil
}

// Versions 列出制品所有版本。
func (s *sqliteArtifactService) Versions(ctx context.Context, req *artifact.VersionsRequest) (*artifact.VersionsResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid versions request: %w", err)
	}
	sess := scopeSession(req.SessionID, req.FileName)

	var vers []int64
	if err := s.db.Model(&Artifact{}).
		Where("app_name = ? AND user_id = ? AND session_id = ? AND file_name = ?",
			req.AppName, req.UserID, sess, req.FileName).
		Order("version ASC").Pluck("version", &vers).Error; err != nil {
		return nil, err
	}
	if vers == nil {
		vers = []int64{}
	}
	return &artifact.VersionsResponse{Versions: vers}, nil
}

// GetArtifactVersion 返回制品指定版本元数据。
func (s *sqliteArtifactService) GetArtifactVersion(ctx context.Context, req *artifact.GetArtifactVersionRequest) (*artifact.GetArtifactVersionResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid get artifact version request: %w", err)
	}
	sess := scopeSession(req.SessionID, req.FileName)

	q := s.db.Where("app_name = ? AND user_id = ? AND session_id = ? AND file_name = ?",
		req.AppName, req.UserID, sess, req.FileName)
	if req.Version != 0 {
		q = q.Where("version = ?", req.Version)
	}
	var row Artifact
	if err := q.Order("version DESC").First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("artifact %s not found", req.FileName)
		}
		return nil, err
	}
	mimeType := ""
	var part genai.Part
	if err := json.Unmarshal([]byte(row.PartJSON), &part); err == nil && part.InlineData != nil {
		mimeType = part.InlineData.MIMEType
	}
	return &artifact.GetArtifactVersionResponse{
		ArtifactVersion: &artifact.ArtifactVersion{
			Version:    row.Version,
			MimeType:   mimeType,
			CreateTime: row.CreatedAt,
		},
	}, nil
}

// FindArtifactByBlobSHA256 按文件 sha256 查找会话内已存在的 artifact（用于去重），返回最新 FileName。
func (s *sqliteArtifactService) FindArtifactByBlobSHA256(appName, userID, sessionID, blobSHA string) (string, bool, error) {
	if s.db == nil || blobSHA == "" {
		return "", false, nil
	}
	var row Artifact
	err := s.db.Where("app_name = ? AND user_id = ? AND session_id = ? AND blob_sha256 = ?",
		appName, userID, sessionID, blobSHA).
		Order("version DESC").First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return row.FileName, true, nil
}
