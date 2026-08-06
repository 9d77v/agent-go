package database

import "testing"

// 回归测试：orchestrator 在回合开始读取 ext 快照（Todos 为空），回合中途 SaveTodos 写入待办，
// 回合结束 SaveSessionExt 用过期快照全字段覆盖 → 待办被清空。修复后 SaveSessionExt 合并
// 数据库中的 Todos，待办应保留。
func TestSaveSessionExt_PreservesTodos(t *testing.T) {
	svc := newTestDBService(t)
	sid := "s-todo-preserve"

	todos := TodoList{
		{ID: 1, Title: "第一步", Status: "not-started"},
		{ID: 2, Title: "第二步", Status: "in-progress"},
	}
	if err := svc.SessionExt.SaveTodos(sid, todos); err != nil {
		t.Fatalf("SaveTodos failed: %v", err)
	}

	// 过期快照：SaveTodos 之前读取的 ext（Todos 为空），仅更新 meta
	stale := &SessionExtModel{SessionID: sid}
	stale.Title = "新标题"
	stale.TokenUsage = `{"total_tokens":123}`
	if err := svc.SessionExt.SaveSessionExt(stale); err != nil {
		t.Fatalf("SaveSessionExt failed: %v", err)
	}

	got, err := svc.SessionExt.GetTodos(sid)
	if err != nil {
		t.Fatalf("GetTodos failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 todos preserved, got %+v", got)
	}
	if got[0].Title != "第一步" || got[1].Status != "in-progress" {
		t.Fatalf("todos content mismatch: %+v", got)
	}
}

// 新会话首次 SaveSessionExt（无既有记录）：应正常创建，Todos 保持传入值（通常为空）。
func TestSaveSessionExt_CreateNew(t *testing.T) {
	svc := newTestDBService(t)
	sid := "s-todo-create"

	ext := &SessionExtModel{SessionID: sid, Title: "t", ApprovalMode: "auto"}
	if err := svc.SessionExt.SaveSessionExt(ext); err != nil {
		t.Fatalf("SaveSessionExt failed: %v", err)
	}
	got, err := svc.SessionExt.GetTodos(sid)
	if err != nil {
		t.Fatalf("GetTodos failed: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty todos for new session, got %+v", got)
	}
	if m := svc.SessionExt.GetSessionExt(sid); m.Title != "t" || m.ApprovalMode != "auto" {
		t.Fatalf("meta not saved: %+v", m)
	}
}

// SaveSessionExt 更新元数据（title/token_usage）不应丢失既有 todos。
func TestSaveSessionExt_UpdateMetaKeepsTodos(t *testing.T) {
	svc := newTestDBService(t)
	sid := "s-todo-meta"

	if err := svc.SessionExt.SaveTodos(sid, TodoList{{ID: 7, Title: "任务", Status: "completed"}}); err != nil {
		t.Fatalf("SaveTodos failed: %v", err)
	}
	// 基于最新读取的 ext 更新 title/token（orchestrator 首次保存路径）
	ext := svc.SessionExt.GetSessionExt(sid)
	ext.Title = "标题"
	ext.TokenUsage = `{"prompt_tokens":1}`
	if err := svc.SessionExt.SaveSessionExt(ext); err != nil {
		t.Fatalf("SaveSessionExt failed: %v", err)
	}
	got, err := svc.SessionExt.GetTodos(sid)
	if err != nil {
		t.Fatalf("GetTodos failed: %v", err)
	}
	if len(got) != 1 || got[0].ID != 7 {
		t.Fatalf("expected todo preserved, got %+v", got)
	}
}

// 显式清空：通过 SaveTodos 空列表清空后，SaveSessionExt 不应把已清空的待办回填回来。
func TestSaveSessionExt_DoesNotResurrectClearedTodos(t *testing.T) {
	svc := newTestDBService(t)
	sid := "s-todo-clear"

	if err := svc.SessionExt.SaveTodos(sid, TodoList{{ID: 1, Title: "a", Status: "not-started"}}); err != nil {
		t.Fatalf("SaveTodos failed: %v", err)
	}
	// 显式清空（todo 工具最后一次全量替换为空列表）
	if err := svc.SessionExt.SaveTodos(sid, TodoList{}); err != nil {
		t.Fatalf("SaveTodos(empty) failed: %v", err)
	}
	// 过期快照再保存 meta，不应把已清空的待办回填
	stale := &SessionExtModel{SessionID: sid, Title: "x"}
	if err := svc.SessionExt.SaveSessionExt(stale); err != nil {
		t.Fatalf("SaveSessionExt failed: %v", err)
	}
	got, err := svc.SessionExt.GetTodos(sid)
	if err != nil {
		t.Fatalf("GetTodos failed: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected todos cleared, got %+v", got)
	}
}
