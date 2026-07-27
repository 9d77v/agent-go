package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LLMService provides multi-provider LLM chat and model listing.
// It supports OpenAI-compatible, Anthropic, and Gemini providers.
type LLMService struct {
	httpClient *http.Client
}

// NewLLMService creates an LLMService with configurable timeout.
func NewLLMService(timeout time.Duration) *LLMService {
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	return &LLMService{
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Chat performs a non-streaming chat completion.
func (s *LLMService) Chat(req *ChatRequest) (*ChatResponse, error) {
	switch req.ProviderID {
	case "anthropic":
		body := BuildAnthropicRequest(req)
		data, _ := json.Marshal(body)
		baseURL := req.ProviderID
		resp, err := s.doRequest("POST", baseURL+"/v1/messages", data)
		if err != nil {
			return nil, err
		}
		return ParseAnthropicResponse(resp)
	case "gemini":
		body := BuildGeminiRequest(req)
		data, _ := json.Marshal(body)
		baseURL := req.ProviderID
		resp, err := s.doRequest("POST", baseURL+"/v1/models/"+req.Model+":generateContent", data)
		if err != nil {
			return nil, err
		}
		return ParseGeminiResponse(resp)
	default:
		// OpenAI-compatible
		body, err := BuildOpenAIRequest(req)
		if err != nil {
			return nil, err
		}
		data, _ := json.Marshal(body)
		baseURL := strings.TrimRight(req.ProviderID, "/")
		resp, err := s.doRequest("POST", baseURL+"/v1/chat/completions", data)
		if err != nil {
			return nil, err
		}
		return ParseOpenAIResponse(resp)
	}
}

// ChatStream performs a streaming chat completion with delta callbacks.
// onContent: called for each content delta.
// onReasoning: called for each reasoning_content delta.
// onToolCallStart: called when a tool call begins.
// onToolCallDelta: called for each tool call arguments delta.
// onToolCallEnd: called when a tool call completes.
// onDone: called when the stream completes.
// onError: called when an error occurs.
func (s *LLMService) ChatStream(
	req *ChatRequest,
	onContent func(string),
	onReasoning func(string),
	onToolCallStart func(string, string),
	onToolCallDelta func(string, string),
	onToolCallEnd func(string, string, string),
	onDone func(),
	onError func(error),
) {
	if req.ProviderID == "anthropic" {
		body := BuildAnthropicRequest(req)
		data, _ := json.Marshal(body)
		baseURL := req.ProviderID
		s.streamSSE("POST", baseURL+"/v1/messages", data, func(line string) {
			s.parseAnthropicStreamLine(line, onContent, onDone)
		}, onError)
		return
	}

	// OpenAI-compatible streaming (includes Gemini via openai-compatible endpoint)
	body, err := BuildOpenAIStreamRequest(req, nil)
	if err != nil {
		onError(err)
		return
	}
	data, _ := json.Marshal(body)
	baseURL := strings.TrimRight(req.ProviderID, "/")
	s.streamSSE("POST", baseURL+"/v1/chat/completions", data, func(line string) {
		s.parseOpenAIStreamLine(line, onContent, onReasoning, onToolCallStart, onToolCallDelta, onToolCallEnd, onDone)
	}, onError)
}

// ListModels fetches available models from an OpenAI-compatible endpoint.
func (s *LLMService) ListModels(baseURL, apiKey string) ([]ModelInfo, error) {
	req, err := http.NewRequest("GET", strings.TrimRight(baseURL, "/")+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list models request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read models response failed: %w", err)
	}

	var result struct {
		Data []struct {
			ID        string `json:"id"`
			CreatedAt int64  `json:"created"`
			OwnedBy   string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse models response failed: %w", err)
	}

	models := make([]ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, ModelInfo{ID: m.ID, CreatedAt: m.CreatedAt, OwnedBy: m.OwnedBy})
	}
	return models, nil
}

// --- Internal helpers ---

func (s *LLMService) doRequest(method, url string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func (s *LLMService) streamSSE(method, url string, body []byte, onLine func(string), onError func(error)) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		onError(err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		onError(fmt.Errorf("stream request failed: %w", err))
		return
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				// Final newline, parse last line
				line = strings.TrimSpace(line)
				if line != "" {
					onLine(line)
				}
			}
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		onLine(line)
	}
}

func (s *LLMService) parseOpenAIStreamLine(
	line string,
	onContent, onReasoning func(string),
	onToolCallStart func(string, string),
	onToolCallDelta func(string, string),
	onToolCallEnd func(string, string, string),
	onDone func(),
) {
	if !strings.HasPrefix(line, "data: ") {
		return
	}
	data := strings.TrimPrefix(line, "data: ")
	if data == "[DONE]" {
		onDone()
		return
	}

	var chunk struct {
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function *struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return
	}
	if len(chunk.Choices) == 0 {
		return
	}
	delta := chunk.Choices[0].Delta
	if delta.ReasoningContent != "" {
		onReasoning(delta.ReasoningContent)
	}
	if delta.Content != "" {
		onContent(delta.Content)
	}
	for _, tc := range delta.ToolCalls {
		if tc.Function == nil {
			continue
		}
		if tc.ID != "" {
			onToolCallStart(tc.ID, tc.Function.Name)
		} else if tc.Function.Arguments != "" {
			onToolCallDelta(fmt.Sprintf("%d", tc.Index), tc.Function.Arguments)
		}
	}
	if chunk.Choices[0].FinishReason == "tool_calls" || chunk.Choices[0].FinishReason == "stop" {
		onDone()
	}
}

func (s *LLMService) parseAnthropicStreamLine(line string, onContent func(string), onDone func()) {
	if !strings.HasPrefix(line, "data: ") {
		return
	}
	data := strings.TrimPrefix(line, "data: ")
	if data == "[DONE]" {
		onDone()
		return
	}
	var event struct {
		Type  string `json:"type"`
		Delta *struct {
			Text string `json:"text"`
		} `json:"delta,omitempty"`
	}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return
	}
	if event.Type == "content_block_delta" && event.Delta != nil {
		onContent(event.Delta.Text)
	}
	if event.Type == "message_stop" {
		onDone()
	}
}
