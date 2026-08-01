package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LLMService 通用 LLM 客户端。
// 支持 Chat Completions (/chat/completions) 与 OpenAI Responses (/responses) 双协议，
// 以及 Anthropic / Gemini 供应商。请求封装统一在此层，供框架消费者调用，
// 上层（如 hhy-code）无需重复实现 HTTP 请求逻辑。
type LLMService struct {
	httpClient *http.Client
}

// NewLLMService 创建 LLMService，可配置超时。
func NewLLMService(timeout time.Duration) *LLMService {
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	return &LLMService{
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Chat 非流式调用 LLM（多供应商路由 + responses 支持）。
func (s *LLMService) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	switch req.ProviderType {
	case "anthropic":
		body := BuildAnthropicRequest(req)
		data, _ := json.Marshal(body)
		base := s.resolveBaseURL(req, "https://api.anthropic.com/v1")
		headers := map[string]string{"x-api-key": req.APIKey, "anthropic-version": "2023-06-01"}
		resp, err := s.doPost(ctx, base+"/messages", headers, data)
		if err != nil {
			return nil, err
		}
		return ParseAnthropicResponse(resp)
	case "gemini":
		body := BuildGeminiRequest(req)
		data, _ := json.Marshal(body)
		base := s.resolveBaseURL(req, "https://generativelanguage.googleapis.com/v1beta")
		url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", base, req.Model, req.APIKey)
		resp, err := s.doPost(ctx, url, nil, data)
		if err != nil {
			return nil, err
		}
		return ParseGeminiResponse(resp)
	default:
		if req.UseResponsesAPI {
			return s.callOpenAIResponses(ctx, req)
		}
		return s.callOpenAICompatible(ctx, req)
	}
}

// ChatStream 流式调用 LLM，通过细粒度回调逐块返回。
// onContent: 内容增量；onReasoning: 思考增量；onToolCallStart/Delta/End: 工具调用流。
func (s *LLMService) ChatStream(
	ctx context.Context,
	req *ChatRequest,
	onContent func(delta string),
	onReasoning func(delta string),
	onToolCallStart func(callID, name string),
	onToolCallDelta func(callID, argsDelta string),
	onToolCallEnd func(callID, name, arguments string),
) (*ChatResponse, error) {
	switch req.ProviderType {
	case "anthropic", "gemini":
		// 这两个供应商暂以非流式回退
		return s.Chat(ctx, req)
	default:
		if req.UseResponsesAPI {
			return s.callOpenAIResponsesStream(ctx, req, onContent, onReasoning, onToolCallStart, onToolCallDelta, onToolCallEnd)
		}
		return s.callOpenAIStream(ctx, req, onContent, onReasoning, onToolCallStart, onToolCallDelta, onToolCallEnd)
	}
}

// ListModels 获取 OpenAI 兼容 API 的可用模型列表。
func (s *LLMService) ListModels(baseURL, apiKey string) ([]ModelInfo, error) {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		return nil, fmt.Errorf("API 地址不能为空")
	}
	httpReq, err := http.NewRequest("GET", base+"/models", nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("获取模型列表失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API 返回错误 (%d)", resp.StatusCode)
	}

	var result struct {
		Data []ModelInfo `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return result.Data, nil
}

// --- 内部辅助 ---

// resolveBaseURL 解析请求 base URL（空时用默认值）。
func (s *LLMService) resolveBaseURL(req *ChatRequest, defaultURL string) string {
	base := strings.TrimRight(req.BaseURL, "/")
	if base == "" {
		base = defaultURL
	}
	return base
}

// doPost 发起 POST JSON 请求，返回响应体；非 200 时返回错误。
func (s *LLMService) doPost(ctx context.Context, url string, headers map[string]string, body []byte) ([]byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API 返回错误 (%d): %s", resp.StatusCode, string(data))
	}
	return data, nil
}

// callOpenAICompatible 非流式调用 Chat Completions API。
func (s *LLMService) callOpenAICompatible(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	bodyMap, err := BuildOpenAIRequest(req)
	if err != nil {
		return nil, fmt.Errorf("构建请求失败: %w", err)
	}
	body, _ := json.Marshal(bodyMap)
	base := s.resolveBaseURL(req, "https://api.openai.com/v1")

	var headers map[string]string
	if req.APIKey != "" {
		headers = map[string]string{"Authorization": "Bearer " + req.APIKey}
	}
	data, err := s.doPost(ctx, base+"/chat/completions", headers, body)
	if err != nil {
		return nil, err
	}
	return ParseOpenAIResponse(data)
}

// callOpenAIResponses 非流式调用 OpenAI Responses API (/responses)。
func (s *LLMService) callOpenAIResponses(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	bodyMap, err := BuildOpenAIResponsesRequest(req)
	if err != nil {
		return nil, fmt.Errorf("构建请求失败: %w", err)
	}
	body, _ := json.Marshal(bodyMap)
	base := s.resolveBaseURL(req, "https://api.openai.com/v1")

	var headers map[string]string
	if req.APIKey != "" {
		headers = map[string]string{"Authorization": "Bearer " + req.APIKey}
	}
	data, err := s.doPost(ctx, base+"/responses", headers, body)
	if err != nil {
		return nil, err
	}
	return ParseOpenAIResponsesResponse(data)
}

// callOpenAIStream 流式调用 Chat Completions API（SSE）。
func (s *LLMService) callOpenAIStream(
	ctx context.Context,
	req *ChatRequest,
	onContent func(delta string),
	onReasoning func(delta string),
	onToolCallStart func(callID, name string),
	onToolCallDelta func(callID, argsDelta string),
	onToolCallEnd func(callID, name, arguments string),
) (*ChatResponse, error) {
	bodyMap, err := BuildOpenAIStreamRequest(req, req.ToolDefs)
	if err != nil {
		return nil, fmt.Errorf("构建流式请求失败: %w", err)
	}
	body, _ := json.Marshal(bodyMap)
	base := s.resolveBaseURL(req, "https://api.openai.com/v1")

	httpReq, err := http.NewRequestWithContext(ctx, "POST", base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if req.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Cache-Control", "no-cache")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API 返回错误 (%d): %s", resp.StatusCode, string(respBody))
	}

	result := &ChatResponse{Model: req.Model}
	type pendingToolCall struct {
		name    string
		id      string
		args    strings.Builder
		started bool // 是否已发送 tool_call_start
	}
	pendingTools := make(map[int]*pendingToolCall)
	reader := bufio.NewReader(resp.Body)
	var fullContent strings.Builder
	var fullReasoning strings.Builder

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("读取流失败: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string                `json:"content"`
					ReasoningContent string                `json:"reasoning_content"`
					ToolCalls        []OpenAIToolCallDelta `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *Usage `json:"usage,omitempty"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.Usage != nil {
			result.Usage = chunk.Usage
		}

		if len(chunk.Choices) > 0 {
			delta := chunk.Choices[0].Delta

			if len(delta.ToolCalls) > 0 {
				for _, tc := range delta.ToolCalls {
					pt, exists := pendingTools[tc.Index]
					if !exists {
						pt = &pendingToolCall{}
						pendingTools[tc.Index] = pt
					}
					if tc.ID != "" {
						pt.id = tc.ID
					}
					if tc.Function != nil {
						if tc.Function.Name != "" {
							pt.name = tc.Function.Name
							if !pt.started {
								onToolCallStart(pt.id, pt.name)
								pt.started = true
							}
						}
						if tc.Function.Arguments != "" {
							onToolCallDelta(pt.id, tc.Function.Arguments)
							pt.args.WriteString(tc.Function.Arguments)
						}
					}
				}
				continue
			}

			if chunk.Choices[0].FinishReason != nil {
				if *chunk.Choices[0].FinishReason == "tool_calls" {
					for _, pt := range pendingTools {
						if pt.name != "" {
							onToolCallEnd(pt.id, pt.name, pt.args.String())
						}
					}
					pendingTools = make(map[int]*pendingToolCall)
					continue
				}
				if *chunk.Choices[0].FinishReason == "stop" {
					break
				}
			}

			if delta.ReasoningContent != "" {
				fullReasoning.WriteString(delta.ReasoningContent)
				onReasoning(delta.ReasoningContent)
			}
			if delta.Content != "" {
				fullContent.WriteString(delta.Content)
				onContent(delta.Content)
			}
		}
	}

	// 报告未处理的 tool_call
	for _, pt := range pendingTools {
		if pt.name != "" {
			onToolCallEnd(pt.id, pt.name, pt.args.String())
		}
	}

	result.Content = fullContent.String()
	result.ReasoningContent = fullReasoning.String()
	return result, nil
}

// callOpenAIResponsesStream 流式调用 OpenAI Responses API（SSE 事件）。
func (s *LLMService) callOpenAIResponsesStream(
	ctx context.Context,
	req *ChatRequest,
	onContent func(delta string),
	onReasoning func(delta string),
	onToolCallStart func(callID, name string),
	onToolCallDelta func(callID, argsDelta string),
	onToolCallEnd func(callID, name, arguments string),
) (*ChatResponse, error) {
	bodyMap, err := BuildOpenAIResponsesRequest(req)
	if err != nil {
		return nil, fmt.Errorf("构建流式请求失败: %w", err)
	}
	body, _ := json.Marshal(bodyMap)
	base := s.resolveBaseURL(req, "https://api.openai.com/v1")

	httpReq, err := http.NewRequestWithContext(ctx, "POST", base+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if req.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Cache-Control", "no-cache")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API 返回错误 (%d): %s", resp.StatusCode, string(respBody))
	}

	result := &ChatResponse{Model: req.Model}
	type pendingToolCall struct {
		name    string
		id      string
		args    strings.Builder
		started bool
	}
	pendingTools := make(map[int]*pendingToolCall)
	reader := bufio.NewReader(resp.Body)
	var fullContent strings.Builder
	var fullReasoning strings.Builder

	finish := false
	for !finish {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("读取流失败: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var event struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
			Index *int   `json:"output_index"`
			Item  struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"item"`
			Response *struct {
				ID    string `json:"id"`
				Model string `json:"model"`
				Usage *struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
					TotalTokens  int `json:"total_tokens"`
				} `json:"usage"`
			} `json:"response"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "response.output_item.added":
			if event.Item.Type == "function_call" && event.Index != nil {
				idx := *event.Index
				pt, exists := pendingTools[idx]
				if !exists {
					pt = &pendingToolCall{}
					pendingTools[idx] = pt
				}
				pt.id = event.Item.ID
				pt.name = event.Item.Name
				if pt.name != "" && !pt.started {
					onToolCallStart(pt.id, pt.name)
					pt.started = true
				}
			}
		case "response.function_call_arguments.delta":
			if event.Index != nil {
				idx := *event.Index
				pt, exists := pendingTools[idx]
				if !exists {
					pt = &pendingToolCall{}
					pendingTools[idx] = pt
				}
				if event.Delta != "" {
					onToolCallDelta(pt.id, event.Delta)
					pt.args.WriteString(event.Delta)
				}
			}
		case "response.function_call_arguments.done":
			if event.Index != nil {
				idx := *event.Index
				pt := pendingTools[idx]
				if pt != nil && pt.name != "" {
					onToolCallEnd(pt.id, pt.name, pt.args.String())
					delete(pendingTools, idx)
				}
			}
		case "response.output_text.delta":
			if event.Delta != "" {
				fullContent.WriteString(event.Delta)
				onContent(event.Delta)
			}
		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			if event.Delta != "" {
				fullReasoning.WriteString(event.Delta)
				onReasoning(event.Delta)
			}
		case "response.completed":
			if event.Response != nil {
				if event.Response.Model != "" {
					result.Model = event.Response.Model
				}
				if event.Response.Usage != nil {
					result.Usage = &Usage{
						PromptTokens:     event.Response.Usage.InputTokens,
						CompletionTokens: event.Response.Usage.OutputTokens,
						TotalTokens:      event.Response.Usage.TotalTokens,
					}
				}
			}
			finish = true
		case "response.failed":
			msg := "responses 流式调用失败"
			if event.Error != nil && event.Error.Message != "" {
				msg = event.Error.Message
			}
			return nil, fmt.Errorf("responses 流式错误: %s", msg)
		}
	}

	// 报告未处理的 tool_call
	for _, pt := range pendingTools {
		if pt.name != "" {
			onToolCallEnd(pt.id, pt.name, pt.args.String())
		}
	}

	result.Content = fullContent.String()
	result.ReasoningContent = fullReasoning.String()
	return result, nil
}
