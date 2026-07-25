package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

// ChangeTracker 通用变更追踪器
type ChangeTracker struct {
	mu      sync.RWMutex
	records []*changeRecord
}

// NewChangeTracker 创建变更追踪器
func NewChangeTracker() *ChangeTracker {
	return &ChangeTracker{records: make([]*changeRecord, 0)}
}

func (t *ChangeTracker) RecordChange(key, backupKey string, isNew bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	status := "modified"
	if isNew {
		status = "created"
	}
	rec := &changeRecord{
		change: AgentFileChange{Path: key, BackupPath: backupKey, Status: status, Timestamp: time.Now().Unix()},
		absPath: backupKey,
	}
	for i, r := range t.records {
		if r.change.Path == key && !r.reverted && !r.accepted {
			t.records[i] = rec
			return
		}
	}
	t.records = append(t.records, rec)
}

func (t *ChangeTracker) GetChanges() []AgentFileChange {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var result []AgentFileChange
	for _, r := range t.records {
		if r.reverted || r.accepted {
			continue
		}
		result = append(result, r.change)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Timestamp > result[j].Timestamp })
	if result == nil {
		result = []AgentFileChange{}
	}
	return result
}

func (t *ChangeTracker) RevertFile(key string, restoreFn func(change AgentFileChange, absPath string) error) error {
	t.mu.Lock()
	var target *changeRecord
	for _, r := range t.records {
		if r.change.Path == key && !r.reverted && !r.accepted {
			target = r
			break
		}
	}
	if target == nil {
		t.mu.Unlock()
		return fmt.Errorf("未找到 %s 的变更记录", key)
	}
	changeCopy := target.change
	absPathCopy := target.absPath
	t.mu.Unlock()
	if err := restoreFn(changeCopy, absPathCopy); err != nil {
		return err
	}
	t.mu.Lock()
	target.reverted = true
	t.mu.Unlock()
	return nil
}

func (t *ChangeTracker) AcceptFile(key string, cleanupFn func(change AgentFileChange) error) error {
	t.mu.Lock()
	var backupToClean string
	found := false
	for _, r := range t.records {
		if r.change.Path == key && !r.reverted && !r.accepted {
			r.accepted = true
			backupToClean = r.change.BackupPath
			found = true
			break
		}
	}
	t.mu.Unlock()
	if !found {
		return fmt.Errorf("未找到 %s 的变更记录", key)
	}
	if cleanupFn != nil {
		_ = cleanupFn(AgentFileChange{BackupPath: backupToClean})
	}
	return nil
}

func (t *ChangeTracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.records = nil
}

func (t *ChangeTracker) AcceptAll(cleanupFn func(change AgentFileChange) error) int {
	t.mu.Lock()
	var toClean []AgentFileChange
	count := 0
	for _, r := range t.records {
		if !r.reverted && !r.accepted {
			r.accepted = true
			if r.change.BackupPath != "" {
				toClean = append(toClean, r.change)
			}
			count++
		}
	}
	t.mu.Unlock()
	for _, c := range toClean {
		if cleanupFn != nil {
			_ = cleanupFn(c)
		}
	}
	return count
}

func (t *ChangeTracker) RevertAll(restoreFn func(change AgentFileChange, absPath string) error) (int, []string) {
	t.mu.Lock()
	type pair struct {
		change  AgentFileChange
		absPath string
	}
	var targets []pair
	for _, r := range t.records {
		if !r.reverted && !r.accepted {
			targets = append(targets, pair{r.change, r.absPath})
		}
	}
	t.mu.Unlock()
	var errs []string
	count := 0
	for _, p := range targets {
		if err := restoreFn(p.change, p.absPath); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p.change.Path, err))
			continue
		}
		count++
		t.mu.Lock()
		for _, r := range t.records {
			if r.change.Path == p.change.Path && !r.reverted && !r.accepted {
				r.reverted = true
				break
			}
		}
		t.mu.Unlock()
	}
	return count, errs
}

func (t *ChangeTracker) GetChangesJSON() json.RawMessage {
	changes := t.GetChanges()
	data, _ := json.Marshal(changes)
	return data
}

// DefaultFileRestore 默认的文件恢复逻辑
func DefaultFileRestore(change AgentFileChange, absPath string) error {
	if change.Status == "created" {
		if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("删除新文件失败: %w", err)
		}
		return nil
	}
	if change.BackupPath == "" {
		return fmt.Errorf("备份路径为空")
	}
	if _, err := os.Stat(change.BackupPath); os.IsNotExist(err) {
		return fmt.Errorf("备份文件不存在: %s", change.BackupPath)
	}
	data, err := os.ReadFile(change.BackupPath)
	if err != nil {
		return fmt.Errorf("读取备份文件失败: %w", err)
	}
	if err := os.WriteFile(absPath, data, 0644); err != nil {
		return fmt.Errorf("恢复文件失败: %w", err)
	}
	os.Remove(change.BackupPath)
	return nil
}

// DefaultFileCleanup 默认的文件清理逻辑
func DefaultFileCleanup(change AgentFileChange) error {
	if change.BackupPath != "" {
		if err := os.Remove(change.BackupPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
