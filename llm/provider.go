package llm

import (
	"encoding/json"
	"fmt"
	"strings"
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
		"model":          req.Model,
		"messages":       msgs,
		"temperature":    req.Temperature,
		"stream":         true,
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
			Role: role,
			Parts: []struct {
				Text string `json:"text"`
			}{part},
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

// ---------- OpenAI Responses API ----------

// BuildOpenAIResponsesRequest 构建 OpenAI Responses API (/responses) 的请求体。
// 将 ChatRequest 的 messages 转换为 responses input 格式：
// user/assistant 文本消息、assistant 工具调用、function_call_output 工具结果、system → instructions。
func BuildOpenAIResponsesRequest(req *ChatRequest) (map[string]any, error) {
	var instructions []string
	input := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch {
		case m.Role == "system":
			if m.Content != "" {
				instructions = append(instructions, m.Content)
			}
		case m.Role == "tool" && m.ToolCallID != "":
			// 工具执行结果
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": m.ToolCallID,
				"output":  m.Content,
			})
		case (m.Role == "assistant" || m.Role == "agent") && len(m.ToolCalls) > 0:
			// assistant 携带工具调用（多轮）
			content := []map[string]any{}
			if m.Content != "" {
				content = append(content, map[string]any{"type": "output_text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				content = append(content, map[string]any{
					"type":      "function_call",
					"id":        tc.ID,
					"name":      tc.Function.Name,
					"arguments": tc.Function.Arguments,
				})
			}
			item := map[string]any{"role": "assistant", "content": content}
			if m.ReasoningContent != "" {
				item["reasoning"] = []map[string]any{{"type": "summary_text", "text": m.ReasoningContent}}
			}
			input = append(input, item)
		default:
			role := m.Role
			if role == "agent" {
				role = "assistant"
			}
			input = append(input, map[string]any{"role": role, "content": m.Content})
		}
	}

	body := map[string]any{
		"model": req.Model,
		"input": input,
	}
	if req.Stream {
		body["stream"] = true
	}
	if req.Temperature != 0 {
		body["temperature"] = req.Temperature
	}
	if len(instructions) > 0 {
		body["instructions"] = strings.Join(instructions, "\n")
	}
	if len(req.ToolDefs) > 0 {
		tools := make([]map[string]any, 0, len(req.ToolDefs))
		for _, td := range req.ToolDefs {
			tools = append(tools, map[string]any{
				"type":        "function",
				"name":        td.Function.Name,
				"description": td.Function.Description,
				"parameters":  td.Function.Parameters,
			})
		}
		body["tools"] = tools
	}
	// 思考模式（Responses API 用 reasoning.effort）
	switch req.Thinking {
	case "deep":
		body["reasoning"] = map[string]any{"effort": "high"}
	case "default":
		body["reasoning"] = map[string]any{"effort": "medium"}
	}
	return body, nil
}

// OpenAIResponsesItem 表示 Responses API 的一个输出项（非流式）。
type OpenAIResponsesItem struct {
	Type      string `json:"type"`
	Role      string `json:"role,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content,omitempty"`
}

// ParseOpenAIResponsesResponse 解析 OpenAI Responses API 的非流式响应。
func ParseOpenAIResponsesResponse(body []byte) (*ChatResponse, error) {
	var raw struct {
		Model  string                `json:"model"`
		Output []OpenAIResponsesItem `json:"output"`
		Usage  *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("解析 OpenAI Responses 响应失败: %w", err)
	}
	result := &ChatResponse{Model: raw.Model}
	if raw.Usage != nil {
		result.Usage = &Usage{
			PromptTokens:     raw.Usage.InputTokens,
			CompletionTokens: raw.Usage.OutputTokens,
			TotalTokens:      raw.Usage.TotalTokens,
		}
	}
	for _, out := range raw.Output {
		for _, c := range out.Content {
			if c.Type == "output_text" {
				result.Content += c.Text
			}
		}
	}
	return result, nil
}
