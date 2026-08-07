package agent

import (
	"encoding/json"
	"testing"

	ftool "github.com/9d77v/agent-go/tool"
)

// 每次 LLM 请求返回 usage 时都应实时持久化，即使中途被打断（max_iterations/取消/错误）。
func TestWrapTokenUsageCallbacks_SavesOnUsage(t *testing.T) {
	var saved []map[string]any
	saveFn := func(tuJSON string) {
		var m map[string]any
		if err := json.Unmarshal([]byte(tuJSON), &m); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		saved = append(saved, m)
	}

	cb := &OrchestratorCallbacks{}
	turnCompleteCalled := false
	cb.OnTurnComplete = func(turnID, sessionID string) { turnCompleteCalled = true }

	WrapTokenUsageCallbacks(cb, saveFn)

	// 两次 LLM 请求各返回 usage（模拟多轮/多次调用）
	cb.OnTokenUsage(ftool.TokenUsageInfo{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120})
	cb.OnTokenUsage(ftool.TokenUsageInfo{PromptTokens: 200, CompletionTokens: 40, TotalTokens: 240})

	if len(saved) != 2 {
		t.Fatalf("expected 2 real-time saves, got %d", len(saved))
	}
	last := saved[len(saved)-1]
	if last["total_tokens"] != float64(240) {
		t.Fatalf("expected last total_tokens=240, got %v", last["total_tokens"])
	}

	// 回合完成也应保存最终值（幂等兜底），且回调不被破坏
	cb.OnTurnComplete("t1", "s1")
	if len(saved) != 3 {
		t.Fatalf("expected 3 saves after turn_complete, got %d", len(saved))
	}
	if !turnCompleteCalled {
		t.Fatal("expected original OnTurnComplete to be called")
	}
}

// total_tokens <= 0 时不写入数据库，避免空值覆盖已有 token 用量。
func TestWrapTokenUsageCallbacks_SkipsZero(t *testing.T) {
	var saved []string
	saveFn := func(tuJSON string) { saved = append(saved, tuJSON) }

	cb := &OrchestratorCallbacks{}
	WrapTokenUsageCallbacks(cb, saveFn)

	cb.OnTokenUsage(ftool.TokenUsageInfo{TotalTokens: 0})
	if len(saved) != 0 {
		t.Fatalf("expected 0 saves for zero usage, got %d", len(saved))
	}

	cb.OnTurnComplete("t1", "s1")
	if len(saved) != 0 {
		t.Fatalf("expected 0 saves after turn_complete with zero usage, got %d", len(saved))
	}
}

// 完整 TokenUsageInfo（含 context_window 及上下文细分字段）应原样持久化，
// 保证历史会话重新打开时的 token 展示与运行中实时展示完全一致。
func TestWrapTokenUsageCallbacks_SavesFullInfo(t *testing.T) {
	var saved map[string]any
	saveFn := func(tuJSON string) {
		if err := json.Unmarshal([]byte(tuJSON), &saved); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
	}

	cb := &OrchestratorCallbacks{}
	WrapTokenUsageCallbacks(cb, saveFn)

	cb.OnTokenUsage(ftool.TokenUsageInfo{
		PromptTokens:      100,
		CompletionTokens:  20,
		TotalTokens:       120,
		CachedTokens:      5,
		ContextWindow:     128000,
		ReservedTokens:    4000,
		SystemTokens:      3000,
		ToolsTokens:       5000,
		MessagesTokens:    10000,
		ToolResultsTokens: 2000,
	})
	if saved == nil {
		t.Fatal("expected save on usage")
	}
	// 基本字段
	if saved["total_tokens"] != float64(120) {
		t.Fatalf("total_tokens = %v, want 120", saved["total_tokens"])
	}
	// 上下文窗口及细分字段必须保留（历史会话与实时展示一致）
	if saved["context_window"] != float64(128000) {
		t.Fatalf("context_window = %v, want 128000", saved["context_window"])
	}
	if saved["reserved_tokens"] != float64(4000) {
		t.Fatalf("reserved_tokens = %v, want 4000", saved["reserved_tokens"])
	}
	if saved["system_tokens"] != float64(3000) {
		t.Fatalf("system_tokens = %v, want 3000", saved["system_tokens"])
	}
	if saved["tools_tokens"] != float64(5000) {
		t.Fatalf("tools_tokens = %v, want 5000", saved["tools_tokens"])
	}
	if saved["messages_tokens"] != float64(10000) {
		t.Fatalf("messages_tokens = %v, want 10000", saved["messages_tokens"])
	}
	if saved["tool_results_tokens"] != float64(2000) {
		t.Fatalf("tool_results_tokens = %v, want 2000", saved["tool_results_tokens"])
	}
}
