package database

import (
	"context"
	"os"
	"testing"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

func newTestDBService(t *testing.T) *DBService {
	t.Helper()
	// 使用 os.MkdirTemp：Windows 下 SQLite 连接会锁定文件导致 t.TempDir() 清理失败，
	// 这里采用 best-effort 清理（忽略错误）。
	dir, err := os.MkdirTemp("", "hhycode-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	svc, err := NewDBService(dir, "hhycode.db")
	if err != nil {
		t.Fatal(err)
	}
	if svc.db == nil {
		t.Fatal("db not initialized")
	}
	return svc
}

func createTestSession(t *testing.T, svc *DBService) session.Session {
	t.Helper()
	ctx := context.Background()
	ss, err := svc.GetSessionService()
	if err != nil {
		t.Fatal(err)
	}
	cr, err := ss.Create(ctx, &session.CreateRequest{AppName: "test", UserID: "u", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	return cr.Session
}

func appendTestEvent(t *testing.T, svc *DBService, sess session.Session, author string, parts []*genai.Part) {
	t.Helper()
	ctx := context.Background()
	ev := session.NewEvent(ctx, "inv")
	ev.Author = author
	ev.LLMResponse.Content = &genai.Content{Parts: parts, Role: author}
	ss, err := svc.GetSessionService()
	if err != nil {
		t.Fatal(err)
	}
	if err := ss.AppendEvent(ctx, sess, ev); err != nil {
		t.Fatal(err)
	}
}

// 悬空 tool call：模型发出函数调用但没有对应工具输出 → 删除该悬空事件。
func TestRepairDanglingSession(t *testing.T) {
	svc := newTestDBService(t)
	sess := createTestSession(t, svc)

	appendTestEvent(t, svc, sess, "user", []*genai.Part{{Text: "hello"}})
	appendTestEvent(t, svc, sess, "model", []*genai.Part{{
		FunctionCall: &genai.FunctionCall{ID: "call_1", Name: "read_file", Args: map[string]any{"path": "a.go"}},
	}})

	removed, err := svc.RepairDanglingSession("s1")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 removed, got %d", removed)
	}
	msgs, err := svc.GetADKSessionEvents("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Fatalf("expected only user message, got %+v", msgs)
	}
}

// 完整会话：调用与输出成对存在 → 不删除任何事件。
func TestRepairDanglingSession_Complete(t *testing.T) {
	svc := newTestDBService(t)
	sess := createTestSession(t, svc)

	appendTestEvent(t, svc, sess, "user", []*genai.Part{{Text: "hello"}})
	appendTestEvent(t, svc, sess, "model", []*genai.Part{{
		FunctionCall: &genai.FunctionCall{ID: "call_1", Name: "read_file", Args: map[string]any{"path": "a.go"}},
	}})
	appendTestEvent(t, svc, sess, "model", []*genai.Part{{
		FunctionResponse: &genai.FunctionResponse{ID: "call_1", Name: "read_file", Response: map[string]any{"output": "ok"}},
	}})
	appendTestEvent(t, svc, sess, "model", []*genai.Part{{Text: "done"}})

	removed, err := svc.RepairDanglingSession("s1")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("expected 0 removed, got %d", removed)
	}
}

// 部分悬空：一次调用两个工具，仅一个返回 → 回滚到 user 之后，删除模型事件 + 已返回的输出。
func TestRepairDanglingSession_Partial(t *testing.T) {
	svc := newTestDBService(t)
	sess := createTestSession(t, svc)

	appendTestEvent(t, svc, sess, "user", []*genai.Part{{Text: "hello"}})
	appendTestEvent(t, svc, sess, "model", []*genai.Part{
		{FunctionCall: &genai.FunctionCall{ID: "call_a", Name: "read_file", Args: map[string]any{"path": "a.go"}}},
		{FunctionCall: &genai.FunctionCall{ID: "call_b", Name: "grep_search", Args: map[string]any{"q": "x"}}},
	})
	appendTestEvent(t, svc, sess, "model", []*genai.Part{{
		FunctionResponse: &genai.FunctionResponse{ID: "call_a", Name: "read_file", Response: map[string]any{"output": "ok"}},
	}})

	removed, err := svc.RepairDanglingSession("s1")
	if err != nil {
		t.Fatal(err)
	}
	// 回滚到 user 之后：删除 model 事件 + call_a 的响应 = 2
	if removed != 2 {
		t.Fatalf("expected 2 removed, got %d", removed)
	}
	msgs, err := svc.GetADKSessionEvents("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Fatalf("expected only user message, got %+v", msgs)
	}
}
