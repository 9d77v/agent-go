// Package agent provides Chat Completions API model adapter for ADK.
// This is needed because ADK's openaimodel uses OpenAI Responses API (/v1/responses),
// which is not supported by providers like DeepSeek that only implement
// the standard Chat Completions API (/v1/chat/completions).
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"log"
	"net/http"
	"strings"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// ChatModel implements model.LLM using the standard OpenAI Chat Completions API.
type ChatModel struct {
	name         string
	apiKey       string
	baseURL      string
	client       *http.Client
	thinkingMode string // "off" | "default" | "deep"
}

// NewChatModel creates a new ChatModel.
// baseURL should be the full endpoint URL, e.g. "https://api.deepseek.com/v1/chat/completions"
func NewChatModel(name, apiKey, baseURL string) *ChatModel {
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/chat/completions") {
		baseURL += "/chat/completions"
	}
	return &ChatModel{
		name:    name,
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{},
	}
}

func (m *ChatModel) Name() string { return m.name }

// SetThinkingMode 设置思考模式。
// "off" → 不启用思考，其他值 → thinking.type=enabled + reasoning_effort 对应
func (m *ChatModel) SetThinkingMode(mode string) {
	m.thinkingMode = mode
}

// GenerateContent implements model.LLM.
func (m *ChatModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	if stream {
		return m.generateStream(ctx, req)
	}
	return m.generate(ctx, req)
}

// ── request / response types ──

type chatMessage struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	Name             string     `json:"name,omitempty"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function toolCallFunc `json:"function"`
}

type toolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatRequest struct {
	Model           string        `json:"model"`
	Messages        []chatMessage `json:"messages"`
	Stream          bool          `json:"stream"`
	Thinking        *thinkingCfg  `json:"thinking,omitempty"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
	Tools           []toolDef     `json:"tools,omitempty"`
}

type thinkingCfg struct {
	Type string `json:"type"`
}

type toolDef struct {
	Type     string      `json:"type"`
	Function toolFuncDef `json:"function"`
}

type toolFuncDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Index   int         `json:"index"`
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

type chatStreamChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role             string     `json:"role,omitempty"`
			Content          string     `json:"content,omitempty"`
			ReasoningContent string     `json:"reasoning_content,omitempty"`
			ToolCalls        []toolCall `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

// ── sync generate ──

func (m *ChatModel) generate(ctx context.Context, req *model.LLMRequest) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		chatReq, err := m.buildChatRequest(req, false)
		if err != nil {
			yield(nil, err)
			return
		}

		body, _ := json.Marshal(chatReq)
		httpReq, err := http.NewRequestWithContext(ctx, "POST", m.baseURL, bytes.NewReader(body))
		if err != nil {
			yield(nil, err)
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if m.apiKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
		}

		resp, err := m.client.Do(httpReq)
		if err != nil {
			yield(nil, fmt.Errorf("chat request failed: %w", err))
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			respBody, _ := io.ReadAll(resp.Body)
			yield(nil, fmt.Errorf("chat API error %d: %s", resp.StatusCode, string(respBody)))
			return
		}

		var chatResp chatResponse
		if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
			yield(nil, fmt.Errorf("decode response: %w", err))
			return
		}

		llmResp := m.toLLMResponse(&chatResp)
		if llmResp != nil {
			if !yield(llmResp, nil) {
				return
			}
		}
	}
}

// ── stream generate ──

func (m *ChatModel) generateStream(ctx context.Context, req *model.LLMRequest) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		chatReq, err := m.buildChatRequest(req, true)
		if err != nil {
			yield(nil, err)
			return
		}

		body, _ := json.Marshal(chatReq)
		httpReq, err := http.NewRequestWithContext(ctx, "POST", m.baseURL, bytes.NewReader(body))
		if err != nil {
			yield(nil, err)
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")
		if m.apiKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
		}

		resp, err := m.client.Do(httpReq)
		if err != nil {
			yield(nil, fmt.Errorf("chat stream failed: %w", err))
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			respBody, _ := io.ReadAll(resp.Body)
			yield(nil, fmt.Errorf("chat stream error %d: %s", resp.StatusCode, string(respBody)))
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		var fullContent strings.Builder
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var chunk chatStreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				log.Printf("[ChatModel] stream parse error: %v", err)
				continue
			}

			if len(chunk.Choices) == 0 {
				continue
			}
			delta := chunk.Choices[0].Delta
			llmResp := &model.LLMResponse{
				Content: &genai.Content{},
				Partial: true,
			}

			if delta.Content != "" {
				fullContent.WriteString(delta.Content)
				llmResp.Content.Parts = append(llmResp.Content.Parts, &genai.Part{Text: delta.Content})
			}

			// 推理/思考内容
			if delta.ReasoningContent != "" {
				llmResp.Content.Parts = append(llmResp.Content.Parts, &genai.Part{
					Text:    delta.ReasoningContent,
					Thought: true,
				})
			}

			// 处理流式工具调用（兼容 DeepSeek 等单 chunk 下发模式）
			for _, tc := range delta.ToolCalls {
				var args map[string]any
				if tc.Function.Arguments != "" {
					json.Unmarshal([]byte(tc.Function.Arguments), &args)
				}
				llmResp.Content.Parts = append(llmResp.Content.Parts, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						ID:   tc.ID,
						Name: tc.Function.Name,
						Args: args,
					},
				})
			}

			if chunk.Choices[0].FinishReason != nil {
				llmResp.TurnComplete = true
				llmResp.Partial = false
				reason := *chunk.Choices[0].FinishReason
				switch reason {
				case "stop":
					llmResp.FinishReason = genai.FinishReasonStop
				case "length":
					llmResp.FinishReason = genai.FinishReasonMaxTokens
				case "tool_calls":
					llmResp.FinishReason = genai.FinishReasonStop
				}
			}

			if chunk.Usage != nil {
				llmResp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
					PromptTokenCount:     int32(chunk.Usage.PromptTokens),
					CandidatesTokenCount: int32(chunk.Usage.CompletionTokens),
					TotalTokenCount:      int32(chunk.Usage.TotalTokens),
				}
				llmResp.TurnComplete = true
				llmResp.Partial = false
			}

			if !yield(llmResp, nil) {
				return
			}
		}

		if err := scanner.Err(); err != nil {
			yield(nil, fmt.Errorf("stream read error: %w", err))
		}
	}
}

// ── helpers ──

func (m *ChatModel) buildChatRequest(req *model.LLMRequest, stream bool) (*chatRequest, error) {
	messages := make([]chatMessage, 0, len(req.Contents))
	for _, c := range req.Contents {
		if c == nil {
			continue
		}
		// 先映射角色：model → assistant（若有 FunctionResponse 后面再覆盖为 tool）
		role := c.Role
		if role == "" {
			role = "user"
		}
		if role == "model" {
			role = "assistant"
		}
		msg := chatMessage{Role: role}

		var texts []string
		var toolMsgs []chatMessage // FunctionResponse → 拆成独立 tool 消息
		for _, part := range c.Parts {
			if part == nil {
				continue
			}
			if part.Text != "" {
				texts = append(texts, part.Text)
			}
			if part.FunctionCall != nil {
				argsJSON, _ := json.Marshal(part.FunctionCall.Args)
				tc := toolCall{
					ID:   part.FunctionCall.ID,
					Type: "function",
					Function: toolCallFunc{
						Name:      part.FunctionCall.Name,
						Arguments: string(argsJSON),
					},
				}
				msg.ToolCalls = append(msg.ToolCalls, tc)
			}
			if part.FunctionResponse != nil {
				respJSON, _ := json.Marshal(part.FunctionResponse.Response)
				toolMsgs = append(toolMsgs, chatMessage{
					Role:       "tool",
					Content:    string(respJSON),
					ToolCallID: part.FunctionResponse.ID,
					Name:       part.FunctionResponse.Name,
				})
			}
		}
		msg.Content = strings.Join(texts, "\n")
		// 跳过完全空的消息（ADK 有时会插入空 user Content）
		hasMainContent := msg.Content != "" || len(msg.ToolCalls) > 0
		if !hasMainContent && len(toolMsgs) == 0 {
			continue
		}
		if hasMainContent {
			messages = append(messages, msg)
		}
		// 每个 FunctionResponse 独立成一条 tool 消息
		messages = append(messages, toolMsgs...)
	}

	chatReq := &chatRequest{
		Model:    m.name,
		Messages: messages,
		Stream:   stream,
	}

	// 思考模式
	if m.thinkingMode != "" && m.thinkingMode != "off" {
		chatReq.Thinking = &thinkingCfg{Type: "enabled"}
		switch m.thinkingMode {
		case "deep":
			chatReq.ReasoningEffort = "max"
		case "default":
			chatReq.ReasoningEffort = "high"
		}
	}

	// 处理 SystemInstruction（系统提示词）
	if req.Config != nil && req.Config.SystemInstruction != nil {
		var sysTexts []string
		for _, part := range req.Config.SystemInstruction.Parts {
			if part != nil && part.Text != "" {
				sysTexts = append(sysTexts, part.Text)
			}
		}
		if len(sysTexts) > 0 {
			chatReq.Messages = append([]chatMessage{{Role: "system", Content: strings.Join(sysTexts, "\n")}}, chatReq.Messages...)
		}
	}

	// 处理 Tools（工具定义 → OpenAI function calling 格式）
	if req.Config != nil {
		for _, tool := range req.Config.Tools {
			if tool == nil || len(tool.FunctionDeclarations) == 0 {
				continue
			}
			for _, decl := range tool.FunctionDeclarations {
				if decl == nil {
					continue
				}
				td := toolDef{
					Type: "function",
					Function: toolFuncDef{
						Name:        decl.Name,
						Description: decl.Description,
					},
				}
				if decl.Parameters != nil {
					td.Function.Parameters = convertSchema(decl.Parameters)
				}
				chatReq.Tools = append(chatReq.Tools, td)
			}
		}
	}

	return chatReq, nil
}

func (m *ChatModel) toLLMResponse(resp *chatResponse) *model.LLMResponse {
	if len(resp.Choices) == 0 {
		return nil
	}
	choice := resp.Choices[0]

	content := &genai.Content{
		Parts: []*genai.Part{},
		Role:  "model",
	}

	// 推理内容优先（确保在文本前）
	if choice.Message.ReasoningContent != "" {
		content.Parts = append(content.Parts, &genai.Part{
			Text:    choice.Message.ReasoningContent,
			Thought: true,
		})
	}

	if choice.Message.Content != "" {
		content.Parts = append(content.Parts, &genai.Part{Text: choice.Message.Content})
	}

	for _, tc := range choice.Message.ToolCalls {
		var args map[string]any
		json.Unmarshal([]byte(tc.Function.Arguments), &args)
		content.Parts = append(content.Parts, &genai.Part{
			FunctionCall: &genai.FunctionCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: args,
			},
		})
	}

	llmResp := &model.LLMResponse{
		Content: content,
		Partial: false,
	}

	if resp.Usage != nil {
		llmResp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     int32(resp.Usage.PromptTokens),
			CandidatesTokenCount: int32(resp.Usage.CompletionTokens),
			TotalTokenCount:      int32(resp.Usage.TotalTokens),
		}
	}

	return llmResp
}

// convertSchema converts *genai.Schema to a plain map for JSON serialization,
// with lowercase type names expected by OpenAI-compatible APIs.
func convertSchema(s *genai.Schema) map[string]any {
	if s == nil {
		return nil
	}
	m := map[string]any{
		"type": convertType(s.Type),
	}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required
	}
	if len(s.Enum) > 0 {
		m["enum"] = s.Enum
	}
	if len(s.Properties) > 0 {
		props := make(map[string]any, len(s.Properties))
		for k, v := range s.Properties {
			props[k] = convertSchema(v)
		}
		m["properties"] = props
	}
	if s.Items != nil {
		m["items"] = convertSchema(s.Items)
	}
	return m
}

var typeMap = map[genai.Type]string{
	genai.TypeUnspecified: "object",
	genai.TypeObject:      "object",
	genai.TypeString:      "string",
	genai.TypeNumber:      "number",
	genai.TypeInteger:     "integer",
	genai.TypeBoolean:     "boolean",
	genai.TypeArray:       "array",
}

func convertType(t genai.Type) string {
	if s, ok := typeMap[t]; ok {
		return s
	}
	return "string"
}

// SimpleChat sends a single user message and returns the text response.
// Useful for simple sub-task calls where the full ADK LLM interface is not needed.
func (m *ChatModel) SimpleChat(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	messages := []chatMessage{}
	if systemPrompt != "" {
		messages = append(messages, chatMessage{Role: "system", Content: systemPrompt})
	}
	messages = append(messages, chatMessage{Role: "user", Content: userMessage})

	req := map[string]any{
		"model":    m.name,
		"messages": messages,
		"stream":   false,
	}

	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", m.baseURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if m.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
	}

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("chat request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("chat API error %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(chatResp.Choices) > 0 {
		return chatResp.Choices[0].Message.Content, nil
	}
	return "", nil
}
