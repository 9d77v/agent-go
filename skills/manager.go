package skills

import (
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

type Manager struct {
	dbStore     *CustomSkillsStore
	workspaceID string
}

func NewManager(db *sql.DB) *Manager {
	m := &Manager{}
	if db != nil {
		m.dbStore = NewCustomSkillsStore(db)
	}
	return m
}

func (m *Manager) SetWorkspaceID(id string)       { m.workspaceID = id }
func (m *Manager) GetWorkspaceID() string         { return m.workspaceID }
func (m *Manager) GetDBStore() *CustomSkillsStore { return m.dbStore }

func (m *Manager) InitBuiltinSkills(fsys fs.FS, dir string) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		log.Printf("[Skills] 读取技能目录失败: %v", err)
		return
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		data, err := fs.ReadFile(fsys, dir+"/"+entry.Name())
		if err != nil {
			continue
		}
		meta, content, body := ParseFrontmatter(string(data))
		Global().Register(&Skill{Meta: meta, Content: content, Body: body, BuiltIn: true, Source: SourceBuiltin})
		count++
	}
	log.Printf("[Skills] 已加载 %d 个内置技能", count)
}

func (m *Manager) LoadWorkspaceSkills(root string) error {
	if root == "" {
		return nil
	}
	Global().ClearSource(SourceWorkspace)
	rootEntries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("读取工作区根目录失败: %w", err)
	}
	count := 0
	for _, entry := range rootEntries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		skillEntries, err := os.ReadDir(filepath.Join(root, entry.Name(), "skills"))
		if err != nil {
			continue
		}
		for _, se := range skillEntries {
			if !se.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, entry.Name(), "skills", se.Name(), "SKILL.md"))
			if err != nil {
				data, err = os.ReadFile(filepath.Join(root, entry.Name(), "skills", se.Name(), "skill.md"))
			}
			if err != nil {
				continue
			}
			meta, content, body := ParseFrontmatter(string(data))
			if meta.Name == "" {
				continue
			}
			Global().Register(&Skill{Meta: meta, Content: content, Body: body, Source: SourceWorkspace})
			count++
		}
	}
	log.Printf("[Skills] 已加载 %d 个工作区技能", count)
	return nil
}

func (m *Manager) LoadCustomSkills() error {
	if m.dbStore == nil {
		return nil
	}
	Global().ClearSource(SourceCustom)
	records, err := m.dbStore.List(m.workspaceID)
	if err != nil {
		return fmt.Errorf("加载自定义技能失败: %w", err)
	}
	count, enabled := 0, 0
	for _, r := range records {
		meta, content, body := ParseFrontmatter(r.Content)
		if meta.Name == "" {
			meta.Name = r.Name
		}
		meta.Enabled = r.Enabled
		meta.Description = r.Description
		Global().Register(&Skill{Meta: meta, Content: content, Body: body, Source: SourceCustom, DBID: r.ID})
		count++
		if r.Enabled {
			enabled++
		}
	}
	log.Printf("[Skills] %d 个已启用（共 %d 个）", enabled, count)
	return nil
}

func (m *Manager) LoadAll(root string, fsys fs.FS, dir string) {
	m.InitBuiltinSkills(fsys, dir)
	m.LoadWorkspaceSkills(root)
	m.LoadCustomSkills()
}

func (m *Manager) ReloadWorkspace(root string) {
	m.LoadWorkspaceSkills(root)
	m.LoadCustomSkills()
}
