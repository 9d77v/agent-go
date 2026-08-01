package llm

import (
	"encoding/json"
	"testing"
)

func TestBuildOpenAIResponsesRequest(t *testing.T) {
	req := &ChatRequest{
		Model: "deepseek-chat",
		Messages: []ChatMessage{
			{Role: "system", Content: "你是助手"},
			{Role: "user", Content: "你好"},
			{Role: "assistant", Content: "好的", ToolCalls: []ToolCallInfo{
				{ID: "call_1", Type: "function", Function: struct{ Name, Arguments string }{Name: "read_file", Arguments: `{"path":"a.go"}`}},
			}},
			{Role: "tool", Content: `{"ok":true}`, ToolCallID: "call_1"},
		},
		Temperature: 0.2,
		ToolDefs: []OpenAIToolDefinition{
			{Type: "function", Function: OpenAIFunctionDef{Name: "read_file", Description: "读取文件"}},
		},
	}

	body, err := BuildOpenAIResponsesRequest(req)
	if err != nil {
		t.Fatalf("BuildOpenAIResponsesRequest err: %v", err)
	}

	if body["model"] != "deepseek-chat" {
		t.Errorf("model = %v", body["model"])
	}
	// system 消息应提取为 instructions
	if body["instructions"] != "你是助手" {
		t.Errorf("instructions = %v", body["instructions"])
	}
	// input 应为 3 项：user、assistant(工具调用)、function_call_output
	input, ok := body["input"].([]map[string]any)
	if !ok {
		t.Fatalf("input type = %T", body["input"])
	}
	if len(input) != 3 {
		t.Fatalf("input len = %d, want 3", len(input))
	}

	// assistant 携带工具调用
	assistant := input[1]
	content, ok := assistant["content"].([]map[string]any)
	if !ok {
		t.Fatalf("assistant content type = %T", assistant["content"])
	}
	foundFC := false
	for _, c := range content {
		if c["type"] == "function_call" && c["name"] == "read_file" && c["id"] == "call_1" {
			foundFC = true
		}
	}
	if !foundFC {
		t.Errorf("assistant content 缺少 function_call: %v", content)
	}

	// 工具执行结果项
	last := input[2]
	if last["type"] != "function_call_output" || last["call_id"] != "call_1" {
		t.Errorf("tool result item = %v", last)
	}

	// 工具定义
	tools, ok := body["tools"].([]map[string]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v", body["tools"])
	}
	if tools[0]["name"] != "read_file" {
		t.Errorf("tool name = %v", tools[0]["name"])
	}
}

func TestBuildOpenAIResponsesRequest_StreamAndThinking(t *testing.T) {
	req := &ChatRequest{
		Model:       "m",
		Messages:    []ChatMessage{{Role: "user", Content: "hi"}},
		Stream:      true,
		Thinking:    "deep",
		Temperature: 0.1,
	}
	body, err := BuildOpenAIResponsesRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if body["stream"] != true {
		t.Errorf("stream = %v", body["stream"])
	}
	if body["temperature"] != 0.1 {
		t.Errorf("temperature = %v", body["temperature"])
	}
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Errorf("reasoning = %v", body["reasoning"])
	}
}

func TestParseOpenAIResponsesResponse(t *testing.T) {
	body := []byte(`{
		"id": "resp_1",
		"model": "deepseek-chat",
		"output": [
			{"type": "message", "role": "assistant", "content": [
				{"type": "output_text", "text": "你好"},
				{"type": "output_text", "text": "世界"}
			]}
		],
		"usage": {"input_tokens": 10, "output_tokens": 20, "total_tokens": 30}
	}`)
	resp, err := ParseOpenAIResponsesResponse(body)
	if err != nil {
		t.Fatalf("ParseOpenAIResponsesResponse err: %v", err)
	}
	if resp.Content != "你好世界" {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.Model != "deepseek-chat" {
		t.Errorf("Model = %q", resp.Model)
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 20 || resp.Usage.TotalTokens != 30 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
}

// 确保请求体可正常 JSON 序列化（模拟真实调用路径）。
func TestBuildOpenAIResponsesRequest_Marshal(t *testing.T) {
	req := &ChatRequest{
		Model:    "m",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}
	body, err := BuildOpenAIResponsesRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := json.Marshal(body); err != nil {
		t.Fatalf("marshal err: %v", err)
	}
}
