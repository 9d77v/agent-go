package skills

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type CustomSkillRecord struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Enabled     bool   `json:"enabled"`
	WorkspaceID string `json:"workspace_id"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type CustomSkillsStore struct{ db *sql.DB }

func NewCustomSkillsStore(db *sql.DB) *CustomSkillsStore {
	s := &CustomSkillsStore{db: db}
	s.InitSchema()
	return s
}

func (s *CustomSkillsStore) InitSchema() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS custom_skills (
		id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT NOT NULL DEFAULT '',
		content TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, workspace_id TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now','localtime')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
	)`)
}

func (s *CustomSkillsStore) List(workspaceID string) ([]CustomSkillRecord, error) {
	var rows *sql.Rows
	var err error
	if workspaceID == "" {
		rows, err = s.db.Query(`SELECT id,name,description,content,enabled,workspace_id,created_at,updated_at FROM custom_skills WHERE workspace_id='' ORDER BY created_at DESC`)
	} else {
		rows, err = s.db.Query(`SELECT id,name,description,content,enabled,workspace_id,created_at,updated_at FROM custom_skills WHERE workspace_id='' OR workspace_id=? ORDER BY created_at DESC`, workspaceID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []CustomSkillRecord
	for rows.Next() {
		var r CustomSkillRecord
		var en int
		rows.Scan(&r.ID, &r.Name, &r.Description, &r.Content, &en, &r.WorkspaceID, &r.CreatedAt, &r.UpdatedAt)
		r.Enabled = en == 1
		records = append(records, r)
	}
	return records, rows.Err()
}

func (s *CustomSkillsStore) ListByWorkspace(workspaceID string) ([]CustomSkillRecord, error) {
	rows, err := s.db.Query(`SELECT id,name,description,content,enabled,workspace_id,created_at,updated_at FROM custom_skills WHERE workspace_id=? ORDER BY created_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []CustomSkillRecord
	for rows.Next() {
		var r CustomSkillRecord
		var en int
		rows.Scan(&r.ID, &r.Name, &r.Description, &r.Content, &en, &r.WorkspaceID, &r.CreatedAt, &r.UpdatedAt)
		r.Enabled = en == 1
		records = append(records, r)
	}
	return records, rows.Err()
}

func (s *CustomSkillsStore) Get(id string) (*CustomSkillRecord, error) {
	var r CustomSkillRecord
	var en int
	err := s.db.QueryRow(`SELECT id,name,description,content,enabled,workspace_id,created_at,updated_at FROM custom_skills WHERE id=?`, id).Scan(&r.ID, &r.Name, &r.Description, &r.Content, &en, &r.WorkspaceID, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Enabled = en == 1
	return &r, nil
}

func (s *CustomSkillsStore) Create(name, content, workspaceID string) (*CustomSkillRecord, error) {
	meta, _, body := ParseFrontmatter(content)
	if meta.Name == "" {
		return nil, fmt.Errorf("技能缺少 name")
	}
	skillName := name
	if skillName == "" {
		skillName = meta.Name
	}
	desc := meta.Description
	if desc == "" && len(body) > 60 {
		desc = body[:60]
	} else if desc == "" {
		desc = body
	}
	id := uuid.Must(uuid.NewV7()).String()
	now := time.Now().Format("2006-01-02 15:04:05")
	_, err := s.db.Exec(`INSERT INTO custom_skills(id,name,description,content,enabled,workspace_id,created_at,updated_at) VALUES(?,?,?,?,1,?,?,?)`, id, skillName, desc, content, workspaceID, now, now)
	if err != nil {
		return nil, fmt.Errorf("创建失败: %w", err)
	}
	return &CustomSkillRecord{ID: id, Name: skillName, Description: desc, Content: content, Enabled: true, WorkspaceID: workspaceID, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *CustomSkillsStore) Update(id, newName, content string) error {
	meta, _, body := ParseFrontmatter(content)
	if meta.Name == "" {
		return fmt.Errorf("技能缺少 name")
	}
	skillName := newName
	if skillName == "" {
		skillName = meta.Name
	}
	desc := meta.Description
	if desc == "" && len(body) > 60 {
		desc = body[:60]
	} else if desc == "" {
		desc = body
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	r, err := s.db.Exec(`UPDATE custom_skills SET name=?,description=?,content=?,updated_at=? WHERE id=?`, skillName, desc, content, now, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return fmt.Errorf("未找到技能: %s", id)
	}
	return nil
}

func (s *CustomSkillsStore) Delete(id string) error {
	r, err := s.db.Exec(`DELETE FROM custom_skills WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return fmt.Errorf("未找到技能: %s", id)
	}
	return nil
}

func (s *CustomSkillsStore) SetEnabled(id string, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	_, err := s.db.Exec(`UPDATE custom_skills SET enabled=?,updated_at=? WHERE id=?`, v, now, id)
	return err
}

func (s *CustomSkillsStore) GetByName(name string) (*CustomSkillRecord, error) {
	var r CustomSkillRecord
	var en int
	err := s.db.QueryRow(`SELECT id,name,description,content,enabled,workspace_id,created_at,updated_at FROM custom_skills WHERE name=?`, name).Scan(&r.ID, &r.Name, &r.Description, &r.Content, &en, &r.WorkspaceID, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Enabled = en == 1
	return &r, nil
}

func (s *CustomSkillsStore) GetByNameAndWorkspace(name, workspaceID string) (*CustomSkillRecord, error) {
	var r CustomSkillRecord
	var en int
	err := s.db.QueryRow(`SELECT id,name,description,content,enabled,workspace_id,created_at,updated_at FROM custom_skills WHERE name=? AND (workspace_id='' OR workspace_id=?)`, name, workspaceID).Scan(&r.ID, &r.Name, &r.Description, &r.Content, &en, &r.WorkspaceID, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Enabled = en == 1
	return &r, nil
}
