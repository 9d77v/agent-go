package agent

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/openaimodel"
	"google.golang.org/genai"
)

// ResponsesModel 基于 OpenAI Responses API (/v1/responses) 的模型适配器。
// 包装 ADK openaimodel 的 model.LLM，实现 model.LLM 接口并额外提供 SimpleChat，
// 用于子 Agent 等无需完整 ADK 编排的简单对话场景。
type ResponsesModel struct {
	llm  model.LLM
	name string
}

// NewResponsesModel 创建基于 Responses API 的模型。
// baseURL 为完整 base url（如 https://api.deepseek.com 或 https://api.openai.com/v1）。
func NewResponsesModel(ctx context.Context, name, apiKey, baseURL string) (*ResponsesModel, error) {
	llm, err := openaimodel.NewModel(ctx, name, &openaimodel.ClientConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("create responses model: %w", err)
	}
	return &ResponsesModel{llm: llm, name: name}, nil
}

// Name 返回模型名称。
func (m *ResponsesModel) Name() string { return m.name }

// GenerateContent 实现 model.LLM，委托底层 openaimodel 模型。
func (m *ResponsesModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return m.llm.GenerateContent(ctx, req, stream)
}

// SimpleChat 发送单条用户消息并返回文本响应。
// 用于子 Agent 等简单对话场景（非流式，走 Responses API）。
func (m *ResponsesModel) SimpleChat(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	req := &model.LLMRequest{
		Config: &genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText(systemPrompt, "system"),
		},
		Contents: []*genai.Content{genai.NewContentFromText(userMessage, "user")},
	}

	var sb strings.Builder
	for resp, err := range m.llm.GenerateContent(ctx, req, false) {
		if err != nil {
			return "", err
		}
		if resp.Content == nil {
			continue
		}
		for _, part := range resp.Content.Parts {
			if part != nil && part.Text != "" {
				sb.WriteString(part.Text)
			}
		}
	}
	return strings.TrimSpace(sb.String()), nil
}
