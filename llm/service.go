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

// defaultLLMTimeout 默认 LLM 请求超时。
const defaultLLMTimeout = 300 * time.Second

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
		timeout = defaultLLMTimeout
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

	resp, err := s.doStreamRequest(ctx, base+"/chat/completions", body, req.APIKey)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	result := &ChatResponse{Model: req.Model}
	tools := newToolCallAccumulator(onToolCallStart, onToolCallDelta, onToolCallEnd)
	var fullContent strings.Builder
	var fullReasoning strings.Builder

	err = forEachSSELine(ctx, bufio.NewReader(resp.Body), func(data string) bool {
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
			return true
		}
		if chunk.Usage != nil {
			result.Usage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			return true
		}

		delta := chunk.Choices[0].Delta
		if len(delta.ToolCalls) > 0 {
			for _, tc := range delta.ToolCalls {
				if tc.Function != nil {
					tools.setMeta(tc.Index, tc.ID, tc.Function.Name)
					tools.appendArgs(tc.Index, tc.Function.Arguments)
				}
			}
			return true
		}

		if chunk.Choices[0].FinishReason != nil {
			switch *chunk.Choices[0].FinishReason {
			case "tool_calls":
				for idx := range tools.pending {
					tools.finish(idx)
				}
				return true
			case "stop":
				return false
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
		return true
	})
	if err != nil {
		return nil, err
	}

	tools.flush()
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

	resp, err := s.doStreamRequest(ctx, base+"/responses", body, req.APIKey)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	result := &ChatResponse{Model: req.Model}
	tools := newToolCallAccumulator(onToolCallStart, onToolCallDelta, onToolCallEnd)
	var fullContent strings.Builder
	var fullReasoning strings.Builder

	var runErr error
	err = forEachSSELine(ctx, bufio.NewReader(resp.Body), func(data string) bool {
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
			return true
		}

		switch event.Type {
		case "response.output_item.added":
			if event.Item.Type == "function_call" && event.Index != nil {
				tools.setMeta(*event.Index, event.Item.ID, event.Item.Name)
			}
		case "response.function_call_arguments.delta":
			if event.Index != nil {
				tools.appendArgs(*event.Index, event.Delta)
			}
		case "response.function_call_arguments.done":
			if event.Index != nil {
				tools.finish(*event.Index)
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
			return false
		case "response.failed":
			msg := "responses 流式调用失败"
			if event.Error != nil && event.Error.Message != "" {
				msg = event.Error.Message
			}
			runErr = fmt.Errorf("responses 流式错误: %s", msg)
			return false
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	if runErr != nil {
		return nil, runErr
	}

	tools.flush()
	result.Content = fullContent.String()
	result.ReasoningContent = fullReasoning.String()
	return result, nil
}

// ── 流式共享基础设施 ──

// doStreamRequest 构造并发送流式 POST 请求（SSE Accept），校验状态码为 200。
// 调用方负责关闭返回的 Body。
func (s *LLMService) doStreamRequest(ctx context.Context, url string, body []byte, apiKey string) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Cache-Control", "no-cache")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	if resp.StatusCode != 200 {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API 返回错误 (%d): %s", resp.StatusCode, string(respBody))
	}
	return resp, nil
}

// forEachSSELine 逐行读取 SSE 流：跳过空行与非 data: 行，对每条 data 调用 fn。
// fn 返回 false 或遇到 [DONE]/EOF 时终止。
func forEachSSELine(ctx context.Context, reader *bufio.Reader, fn func(data string) bool) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("读取流失败: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			return nil
		}
		if !fn(data) {
			return nil
		}
	}
}

// pendingToolCall 累积中的工具调用（按 chunk/event index 聚合增量参数）。
type pendingToolCall struct {
	name    string
	id      string
	args    strings.Builder
	started bool // 是否已发送 tool_call_start
}

// toolCallAccumulator 工具调用增量状态机：跨 SSE 事件聚合 name/args，
// 首次出现 name 时触发 onStart、参数增量触发 onDelta、完成时触发 onEnd。
type toolCallAccumulator struct {
	pending map[int]*pendingToolCall
	onStart func(callID, name string)
	onDelta func(callID, argsDelta string)
	onEnd   func(callID, name, arguments string)
}

func newToolCallAccumulator(
	onStart func(callID, name string),
	onDelta func(callID, argsDelta string),
	onEnd func(callID, name, arguments string),
) *toolCallAccumulator {
	return &toolCallAccumulator{
		pending: make(map[int]*pendingToolCall),
		onStart: onStart,
		onDelta: onDelta,
		onEnd:   onEnd,
	}
}

func (a *toolCallAccumulator) ensure(idx int) *pendingToolCall {
	pt, exists := a.pending[idx]
	if !exists {
		pt = &pendingToolCall{}
		a.pending[idx] = pt
	}
	return pt
}

// setMeta 记录 id/name；name 首次出现时触发 onStart。
func (a *toolCallAccumulator) setMeta(idx int, id, name string) {
	pt := a.ensure(idx)
	if id != "" {
		pt.id = id
	}
	if name != "" {
		pt.name = name
		if !pt.started {
			a.onStart(pt.id, pt.name)
			pt.started = true
		}
	}
}

// appendArgs 追加参数增量并触发 onDelta。
func (a *toolCallAccumulator) appendArgs(idx int, argsDelta string) {
	if argsDelta == "" {
		return
	}
	pt := a.ensure(idx)
	a.onDelta(pt.id, argsDelta)
	pt.args.WriteString(argsDelta)
}

// finish 完成指定 index 的调用（触发 onEnd 并从 pending 移除）。
func (a *toolCallAccumulator) finish(idx int) {
	pt := a.pending[idx]
	if pt != nil && pt.name != "" {
		a.onEnd(pt.id, pt.name, pt.args.String())
		delete(a.pending, idx)
	}
}

// flush 报告所有未完成的调用（流结束兜底）。
func (a *toolCallAccumulator) flush() {
	for _, pt := range a.pending {
		if pt.name != "" {
			a.onEnd(pt.id, pt.name, pt.args.String())
		}
	}
}
