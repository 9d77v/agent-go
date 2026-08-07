package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// newChatCompletionsSSEServer 返回模拟 /chat/completions SSE 的 httptest 服务器。
// 返回顺序：给定 chunks 逐个作为 data 行，最后追加 [DONE]。
func newChatCompletionsSSEServer(t *testing.T, chunks ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	return srv
}

// TestChatStream_OpenAICompletions 特征化 Chat Completions 流式：
// 内容/思考增量逐块转发、流式工具调用按 index 聚合（start/delta/end）、finish_reason=tool_calls 收尾。
func TestChatStream_OpenAICompletions(t *testing.T) {
	srv := newChatCompletionsSSEServer(t,
		`{"choices":[{"delta":{"content":"hel"}}]}`,
		`{"choices":[{"delta":{"content":"lo"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"think1"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read_file","arguments":"{\"path\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"/tmp/x.txt\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	defer srv.Close()

	svc := NewLLMService(0)
	var content, reasoning strings.Builder
	var mu sync.Mutex
	var toolStarts []string
	var toolDeltas int
	var toolEnds []string

	resp, err := svc.ChatStream(context.Background(), &ChatRequest{
		Model:        "test",
		BaseURL:      srv.URL,
		ProviderType: "openai",
		Messages:     []ChatMessage{{Role: "user", Content: "hi"}},
	}, func(d string) {
		mu.Lock()
		content.WriteString(d)
		mu.Unlock()
	}, func(d string) {
		mu.Lock()
		reasoning.WriteString(d)
		mu.Unlock()
	}, func(callID, name string) {
		mu.Lock()
		toolStarts = append(toolStarts, callID+":"+name)
		mu.Unlock()
	}, func(callID, argsDelta string) {
		mu.Lock()
		toolDeltas++
		mu.Unlock()
	}, func(callID, name, args string) {
		mu.Lock()
		toolEnds = append(toolEnds, callID+":"+name+":"+args)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	if content.String() != "hello" {
		t.Errorf("content = %q, want hello", content.String())
	}
	if reasoning.String() != "think1" {
		t.Errorf("reasoning = %q, want think1", reasoning.String())
	}
	if len(toolStarts) != 1 || toolStarts[0] != "call_1:read_file" {
		t.Errorf("toolStarts = %v, want [call_1:read_file]", toolStarts)
	}
	if toolDeltas != 2 {
		t.Errorf("toolDeltas = %d, want 2", toolDeltas)
	}
	if len(toolEnds) != 1 || toolEnds[0] != `call_1:read_file:{"path":"/tmp/x.txt"}` {
		t.Errorf("toolEnds = %v, want [call_1:read_file:{\"path\":\"/tmp/x.txt\"}]", toolEnds)
	}
	if resp.Content != "hello" {
		t.Errorf("resp.Content = %q, want hello", resp.Content)
	}
}

// newResponsesSSEServer 返回模拟 /responses SSE 事件的 httptest 服务器。
func newResponsesSSEServer(t *testing.T, events ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
		}
	}))
	return srv
}

// TestChatStream_OpenAIResponses 特征化 Responses 流式事件：
// output_item.added → 工具 start，arguments.delta 聚合，done → end，output_text.delta → 内容，
// completed → model/usage 与终止。
func TestChatStream_OpenAIResponses(t *testing.T) {
	srv := newResponsesSSEServer(t,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","name":"read_file"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"path\":"}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"\"/tmp/x.txt\"}"}`,
		`{"type":"response.function_call_arguments.done","output_index":0}`,
		`{"type":"response.output_text.delta","delta":"hi"}`,
		`{"type":"response.completed","response":{"id":"r_1","model":"test-model","usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30}}}`,
	)
	defer srv.Close()

	svc := NewLLMService(0)
	var content strings.Builder
	var toolStarts []string
	var toolEnds []string

	resp, err := svc.ChatStream(context.Background(), &ChatRequest{
		Model:           "test",
		BaseURL:         srv.URL,
		ProviderType:    "openai",
		UseResponsesAPI: true,
		Messages:        []ChatMessage{{Role: "user", Content: "hi"}},
	}, func(d string) { content.WriteString(d) },
		func(d string) {},
		func(callID, name string) { toolStarts = append(toolStarts, callID+":"+name) },
		func(callID, argsDelta string) {},
		func(callID, name, args string) { toolEnds = append(toolEnds, callID+":"+name+":"+args) },
	)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	if content.String() != "hi" {
		t.Errorf("content = %q, want hi", content.String())
	}
	if len(toolStarts) != 1 || toolStarts[0] != "fc_1:read_file" {
		t.Errorf("toolStarts = %v, want [fc_1:read_file]", toolStarts)
	}
	if len(toolEnds) != 1 || toolEnds[0] != `fc_1:read_file:{"path":"/tmp/x.txt"}` {
		t.Errorf("toolEnds = %v", toolEnds)
	}
	if resp.Model != "test-model" {
		t.Errorf("resp.Model = %q, want test-model", resp.Model)
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 20 || resp.Usage.TotalTokens != 30 {
		t.Errorf("resp.Usage = %+v, want 10/20/30", resp.Usage)
	}
}

// TestChatStream_OpenAIResponsesFailed 特征化 response.failed 事件 → 返回错误。
func TestChatStream_OpenAIResponsesFailed(t *testing.T) {
	srv := newResponsesSSEServer(t,
		`{"type":"response.failed","error":{"message":"rate limited"}}`,
	)
	defer srv.Close()

	svc := NewLLMService(0)
	_, err := svc.ChatStream(context.Background(), &ChatRequest{
		Model: "test", BaseURL: srv.URL, ProviderType: "openai", UseResponsesAPI: true,
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, func(d string) {}, func(d string) {}, func(a, b string) {}, func(a, b string) {}, func(a, b, c string) {})
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("err = %v, want contains rate limited", err)
	}
}
