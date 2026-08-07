package agent

import (
	"context"
	"iter"
	"strings"
	"testing"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// streamMockModel 模拟流式 LLM：产出 partial 增量块 + final 聚合事件。
type streamMockModel struct{}

func (streamMockModel) Name() string { return "stream-mock" }

func (streamMockModel) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		// 两个 partial 增量块 + 一个 final 聚合事件（含完整文本）
		if !yield(&model.LLMResponse{Content: &genai.Content{Parts: []*genai.Part{{Text: "hel"}}}, Partial: true}, nil) {
			return
		}
		if !yield(&model.LLMResponse{Content: &genai.Content{Parts: []*genai.Part{{Text: "lo"}}}, Partial: true}, nil) {
			return
		}
		yield(&model.LLMResponse{Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: "hello"}}}}, nil)
	}
}

// TestAdkRunnerStreaming_Dedup 验证流式模式下：
// 1) partial 增量逐块转发、final 聚合文本跳过 → 前端文本不重复（hel+lo=hello，而非 hel lo hello）；
// 2) MaxIterations 只统计 non-partial（本例仅 1 个 final），长回复不会被 partial 块提前截断；
// 3) 默认 StreamingMode 为 SSE。
func TestAdkRunnerStreaming_Dedup(t *testing.T) {
	svc := session.InMemoryService()

	agt, err := llmagent.New(llmagent.Config{Name: "main", Model: streamMockModel{}})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	r, err := NewAdkRunner(AdkRunnerConfig{
		AppName:        "test",
		Agent:          agt,
		SessionService: svc,
		// 若按事件数计数（2 partial + 1 final），1 会触发 break；
		// 只统计 non-partial（1 个 final）则不应截断。
		MaxIterations: 1,
	})
	if err != nil {
		t.Fatalf("create runner: %v", err)
	}
	if r.config.StreamingMode != adkagent.StreamingModeSSE {
		t.Fatalf("default StreamingMode = %q, want %q", r.config.StreamingMode, adkagent.StreamingModeSSE)
	}

	var got strings.Builder
	callbacks := &OrchestratorCallbacks{}
	callbacks.OnContentDelta = func(_ string, delta string) { got.WriteString(delta) }
	callbacks.OnTurnComplete = func(_, _ string) {}
	callbacks.OnError = func(_, _, msg string) { t.Logf("onError: %s", msg) }

	if err := r.Run(context.Background(), "u", "s1", "hi", callbacks); err != nil {
		t.Fatalf("run: %v", err)
	}

	// 1) 文本去重：最终拼接应为完整文本一次，而非 partial + final 重复
	if got.String() != "hello" {
		t.Errorf("streamed text = %q, want %q（partial 增量/final 聚合去重失败）", got.String(), "hello")
	}

	// 2) iterCount 只统计 non-partial：MaxIterations=1 未触发截断（若按事件数则已 break）
	// 3) 通过 onError 未收到 max_iterations 隐含验证（上面已断言文本完整）
}

// TestAdkRunnerStreaming_FinalTextSkipped 显式验证 final 聚合事件的完整文本不会重复推送。
func TestAdkRunnerStreaming_FinalTextSkipped(t *testing.T) {
	svc := session.InMemoryService()
	agt, err := llmagent.New(llmagent.Config{Name: "main", Model: streamMockModel{}})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	r, err := NewAdkRunner(AdkRunnerConfig{AppName: "test", Agent: agt, SessionService: svc})
	if err != nil {
		t.Fatalf("create runner: %v", err)
	}

	var deltas []string
	callbacks := &OrchestratorCallbacks{}
	callbacks.OnContentDelta = func(_ string, delta string) { deltas = append(deltas, delta) }
	callbacks.OnTurnComplete = func(_, _ string) {}
	callbacks.OnError = func(_, _, msg string) { t.Logf("onError: %s", msg) }

	if err := r.Run(context.Background(), "u", "s1", "hi", callbacks); err != nil {
		t.Fatalf("run: %v", err)
	}

	// partial 增量块应有 2 次推送（hel / lo），final 聚合 "hello" 不应出现
	if len(deltas) != 2 || deltas[0] != "hel" || deltas[1] != "lo" {
		t.Errorf("deltas = %v, want [hel lo]（final 聚合文本不应重复推送）", deltas)
	}
}
