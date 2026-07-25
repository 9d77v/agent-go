package llm

import (
	"encoding/json"
	"fmt"
)

// ---------- OpenAI 兼容内部类型 ----------

type OpenAIChatMessage struct {
	Role             string         `json:"role"`
	Content          string         `json:"content"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	ToolCalls        []ToolCallInfo `json:"tool_calls,omitempty"`
}

type OpenAIChatRequest struct {
	Model         string                 `json:"model"`
	Messages      []OpenAIChatMessage    `json:"messages"`
	Temperature   float64                `json:"temperature,omitempty"`
	Stream        bool                   `json:"stream,omitempty"`
	StreamOptions *OpenAIStreamOptions   `json:"stream_options,omitempty"`
	Tools         []OpenAIToolDefinition `json:"tools,omitempty"`
}

type OpenAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type OpenAIToolDefinition struct {
	Type     string            `json:"type"`
	Function OpenAIFunctionDef `json:"function"`
}

type OpenAIFunctionDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type OpenAIToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function *struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
}

type OpenAIChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage,omitempty"`
}

// BuildOpenAIRequest 构建 OpenAI 兼容的非流式请求体
func BuildOpenAIRequest(req *ChatRequest) (map[string]any, error) {
	msgs := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		msg := map[string]any{"role": m.Role}
		if len(m.ToolCalls) > 0 {
			msg["content"] = nil
		} else {
			msg["content"] = m.Content
		}
		if m.ReasoningContent != "" {
			msg["reasoning_content"] = m.ReasoningContent
		}
		if m.ToolCallID != "" {
			msg["tool_call_id"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			msg["tool_calls"] = m.ToolCalls
		}
		msgs = append(msgs, msg)
	}
	body := map[string]any{
		"model":       req.Model,
		"messages":    msgs,
		"temperature": req.Temperature,
		"stream":      req.Stream,
	}
	switch req.Thinking {
	case "deep":
		body["thinking"] = map[string]any{"type": "enabled", "reasoning_effort": "high"}
	case "default":
		body["thinking"] = map[string]any{"type": "enabled"}
	}
	return body, nil
}

// BuildOpenAIStreamRequest 构建流式请求体
func BuildOpenAIStreamRequest(req *ChatRequest, tools []OpenAIToolDefinition) (map[string]any, error) {
	msgs := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		msg := map[string]any{"role": m.Role}
		if len(m.ToolCalls) > 0 {
			msg["content"] = nil
		} else {
			msg["content"] = m.Content
		}
		if m.ReasoningContent != "" {
			msg["reasoning_content"] = m.ReasoningContent
		}
		if m.ToolCallID != "" {
			msg["tool_call_id"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			msg["tool_calls"] = m.ToolCalls
		}
		msgs = append(msgs, msg)
	}
	body := map[string]any{
		"model":       req.Model,
		"messages":    msgs,
		"temperature": req.Temperature,
		"stream":      true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	switch req.Thinking {
	case "deep":
		body["thinking"] = map[string]any{"type": "enabled", "reasoning_effort": "high"}
	case "default":
		body["thinking"] = map[string]any{"type": "enabled"}
	}
	return body, nil
}

type AnthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func BuildAnthropicRequest(req *ChatRequest) map[string]any {
	messages := make([]AnthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := m.Role
		if role == "agent" {
			role = "assistant"
		}
		messages = append(messages, AnthropicMessage{Role: role, Content: m.Content})
	}
	return map[string]any{
		"model":       req.Model,
		"messages":    messages,
		"max_tokens":  4096,
		"temperature": req.Temperature,
	}
}

type GeminiContent struct {
	Role  string `json:"role"`
	Parts []struct {
		Text string `json:"text"`
	} `json:"parts"`
}

func BuildGeminiRequest(req *ChatRequest) map[string]any {
	var contents []GeminiContent
	for _, m := range req.Messages {
		role := m.Role
		if role == "agent" {
			role = "model"
		}
		part := struct {
			Text string `json:"text"`
		}{Text: m.Content}
		contents = append(contents, GeminiContent{
			Role:  role,
			Parts: []struct{ Text string `json:"text"` }{part},
		})
	}
	return map[string]any{"contents": contents}
}

func ParseOpenAIResponse(body []byte) (*ChatResponse, error) {
	var openAIResp OpenAIChatResponse
	if err := json.Unmarshal(body, &openAIResp); err != nil {
		return nil, fmt.Errorf("解析 OpenAI 响应失败: %w", err)
	}
	result := &ChatResponse{Model: openAIResp.Model, Usage: openAIResp.Usage}
	if len(openAIResp.Choices) > 0 {
		result.Content = openAIResp.Choices[0].Message.Content
	}
	return result, nil
}

func ParseAnthropicResponse(body []byte) (*ChatResponse, error) {
	var anthropicResp struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage *Usage `json:"usage"`
	}
	if err := json.Unmarshal(body, &anthropicResp); err != nil {
		return nil, fmt.Errorf("解析 Anthropic 响应失败: %w", err)
	}
	result := &ChatResponse{Model: anthropicResp.Model, Usage: anthropicResp.Usage}
	for _, c := range anthropicResp.Content {
		result.Content += c.Text
	}
	return result, nil
}

func ParseGeminiResponse(body []byte) (*ChatResponse, error) {
	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
				Role string `json:"role"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata *Usage `json:"usageMetadata"`
	}
	if err := json.Unmarshal(body, &geminiResp); err != nil {
		return nil, fmt.Errorf("解析 Gemini 响应失败: %s", string(body))
	}
	result := &ChatResponse{Model: ""}
	if len(geminiResp.Candidates) > 0 {
		for _, part := range geminiResp.Candidates[0].Content.Parts {
			result.Content += part.Text
		}
	}
	result.Usage = geminiResp.UsageMetadata
	return result, nil
}
