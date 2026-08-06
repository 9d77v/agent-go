package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// newCaptureServer 启动捕获请求体的 mock Chat API 服务器，返回固定响应。
func newCaptureServer(t *testing.T, captured *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*captured = b
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"MOCK"}}]}`))
	}))
}

// extractImageURL 从请求体中提取 image_url.url 值（content 可能是 string 或多模态数组）。
func extractImageURL(t *testing.T, body []byte) string {
	t.Helper()
	var req struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("解析请求体失败: %v\n%s", err, string(body))
	}
	var parts []struct {
		Type     string `json:"type"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	for _, m := range req.Messages {
		if len(m.Content) == 0 || m.Content[0] != '[' {
			continue // 纯文本 content（如 system）
		}
		if err := json.Unmarshal(m.Content, &parts); err != nil {
			continue
		}
		for _, c := range parts {
			if c.Type == "image_url" {
				return c.ImageURL.URL
			}
		}
	}
	return ""
}

// TestSimpleChatWithImageData 验证 base64 data URI 形式发送给 Chat API 的请求体。
func TestSimpleChatWithImageData(t *testing.T) {
	var captured []byte
	srv := newCaptureServer(t, &captured)
	defer srv.Close()

	img := []byte("fake-webp-bytes")
	m := NewChatModel("qwen3.6-vl", "sk-test", srv.URL)
	if _, err := m.SimpleChatWithImageData(context.Background(), "系统", "描述图片", img, "image/webp"); err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	url := extractImageURL(t, captured)
	want := "data:image/webp;base64," + base64.StdEncoding.EncodeToString(img)
	if url != want {
		t.Errorf("image_url.url = %q, want %q", url, want)
	}
	if !strings.HasPrefix(url, "data:image/webp;base64,") {
		t.Errorf("url 不是 base64 data URI: %q", url)
	}
}

// TestRealLMStudio_ImageFormats 真连 LM Studio 对比多种 image_url 格式，确认哪种被接受。
// 环境变量：
//
//	LML_BASE_URL  = http://10.6.0.5:1234/v1/chat/completions
//	LML_MODEL     = qwen/qwen3.6-35b-a3b
//	LML_IMAGE_URL = https://hhy-code-oss.oss-cn-shanghai.aliyuncs.com/default/artifacts/images/...
//
// 运行：LML_BASE_URL=... LML_MODEL=... LML_IMAGE_URL=... go test -v -run TestRealLMStudio_ImageFormats ./agent/
func TestRealLMStudio_ImageFormats(t *testing.T) {
	baseURL := os.Getenv("LML_BASE_URL")
	model := os.Getenv("LML_MODEL")
	imageURL := os.Getenv("LML_IMAGE_URL")
	apiKey := os.Getenv("LML_API_KEY")
	if baseURL == "" || model == "" || imageURL == "" {
		t.Skip("set LML_BASE_URL / LML_MODEL / LML_IMAGE_URL to run")
	}
	if apiKey == "" {
		t.Log("未设置 LML_API_KEY，使用无效 token（将返回 401）")
		apiKey = "x"
	}

	resp, err := http.Get(imageURL)
	if err != nil {
		t.Fatalf("下载图片失败: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	t.Logf("图片 Content-Type=%q size=%d", resp.Header.Get("Content-Type"), len(data))
	b64 := base64.StdEncoding.EncodeToString(data)

	ctx := context.Background()
	m := NewChatModel(model, apiKey, baseURL)

	formats := []struct {
		name string
		url  string
	}{
		{"dataURI-webp", "data:image/webp;base64," + b64},
		{"raw-base64", b64},
		{"dataURI-jpeg", "data:image/jpeg;base64," + b64},
		{"dataURI-png", "data:image/png;base64," + b64},
	}
	for _, f := range formats {
		got, err := m.SimpleChatWithImageURL(ctx, "系统", "用一句话描述这张图片", f.url)
		if err != nil {
			t.Logf("[%s] ❌ err=%v", f.name, err)
		} else {
			t.Logf("[%s] ✅ 返回: %s", f.name, got)
		}
	}
}
