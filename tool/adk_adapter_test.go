package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	adkagent "google.golang.org/adk/v2/agent"
	adktool "google.golang.org/adk/v2/tool"
	"google.golang.org/genai"
)

// nilAgentContext 最小 mock：嵌入空接口满足 agent.Context，
// 测试中的 handler 不使用 ctx，避免实现全部接口方法。
type nilAgentContext struct {
	adkagent.Context
}

var _ adkagent.Context = nilAgentContext{}

// TestNewAdkTool_SchemaConversion 特征化 NewAdkTool 的 OpenAI 风格参数 → genai.Schema 转换：
// 基础类型、description、enum、数组 items、嵌套 object、required。
func TestNewAdkTool_SchemaConversion(t *testing.T) {
	cfg := AdkToolConfig{
		Name:        "read_file",
		Description: "读取文件",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string", "description": "文件路径"},
				"line_start": map[string]any{"type": "integer"},
				"mode":       map[string]any{"type": "string", "enum": []any{"read", "write"}},
				"tags":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"nested": map[string]any{"type": "object", "properties": map[string]any{
					"flag": map[string]any{"type": "boolean"},
				}},
			},
			"required": []any{"path"},
		},
	}
	th := ToolHandler(func(_ context.Context, _ json.RawMessage) *ToolResult {
		return &ToolResult{Success: true, Output: "ok"}
	})
	tool := NewAdkTool(cfg, th)
	at, ok := tool.(*adkTool)
	if !ok {
		t.Fatalf("NewAdkTool returned %T, want *adkTool", tool)
	}
	decl := at.Declaration()

	if decl.Name != "read_file" || decl.Description != "读取文件" {
		t.Errorf("decl name/desc = %q/%q", decl.Name, decl.Description)
	}
	if decl.Parameters == nil || decl.Parameters.Type != genai.TypeObject {
		t.Fatalf("parameters missing or not object: %+v", decl.Parameters)
	}
	props := decl.Parameters.Properties
	if props["path"] == nil || props["path"].Type != genai.TypeString || props["path"].Description != "文件路径" {
		t.Errorf("path prop = %+v, want string 文件路径", props["path"])
	}
	if props["line_start"] == nil || props["line_start"].Type != genai.TypeInteger {
		t.Errorf("line_start prop = %+v, want integer", props["line_start"])
	}
	if len(props["mode"].Enum) != 2 || props["mode"].Enum[0] != "read" || props["mode"].Enum[1] != "write" {
		t.Errorf("mode enum = %v, want [read write]", props["mode"].Enum)
	}
	if props["tags"].Items == nil || props["tags"].Items.Type != genai.TypeString {
		t.Errorf("tags items = %+v, want string", props["tags"].Items)
	}
	if props["nested"].Properties == nil || props["nested"].Properties["flag"].Type != genai.TypeBoolean {
		t.Errorf("nested prop = %+v, want boolean flag", props["nested"].Properties)
	}
	if len(decl.Parameters.Required) != 1 || decl.Parameters.Required[0] != "path" {
		t.Errorf("required = %v, want [path]", decl.Parameters.Required)
	}
}

// TestAdkTool_Run_Success 特征化成功路径：handler 返回值映射为 {"output": ...}。
func TestAdkTool_Run_Success(t *testing.T) {
	th := ToolHandler(func(_ context.Context, args json.RawMessage) *ToolResult {
		var in struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(args, &in)
		return &ToolResult{Success: true, Output: "content of " + in.Path}
	})
	tool := NewAdkTool(AdkToolConfig{Name: "read_file", Parameters: map[string]any{}}, th)
	at := tool.(*adkTool)

	res, err := at.Run(nilAgentContext{}, map[string]any{"path": "/tmp/x.txt"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res["output"] != "content of /tmp/x.txt" {
		t.Errorf("output = %v", res["output"])
	}
}

// TestAdkTool_Run_Failure 特征化失败路径：Success=false → 返回 error。
func TestAdkTool_Run_Failure(t *testing.T) {
	th := ToolHandler(func(_ context.Context, _ json.RawMessage) *ToolResult {
		return &ToolResult{Success: false, Error: "boom"}
	})
	tool := NewAdkTool(AdkToolConfig{Name: "x", Parameters: map[string]any{}}, th)
	at := tool.(*adkTool)

	_, err := at.Run(nilAgentContext{}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want contains boom", err)
	}
}

// TestAdkTool_Run_ConfirmationRequired 特征化 HITL：ConfirmationRequired → ErrConfirmationRequired。
func TestAdkTool_Run_ConfirmationRequired(t *testing.T) {
	th := ToolHandler(func(_ context.Context, _ json.RawMessage) *ToolResult {
		return &ToolResult{Success: true, ConfirmationRequired: true}
	})
	tool := NewAdkTool(AdkToolConfig{Name: "x", Parameters: map[string]any{}}, th)
	at := tool.(*adkTool)

	_, err := at.Run(nilAgentContext{}, map[string]any{})
	if err != adktool.ErrConfirmationRequired {
		t.Errorf("err = %v, want ErrConfirmationRequired", err)
	}
}

// TestAdkTool_Run_ConfirmationRejected 特征化拒绝路径：ConfirmationRejected → ErrConfirmationRejected。
func TestAdkTool_Run_ConfirmationRejected(t *testing.T) {
	th := ToolHandler(func(_ context.Context, _ json.RawMessage) *ToolResult {
		return &ToolResult{Success: true, ConfirmationRejected: true}
	})
	tool := NewAdkTool(AdkToolConfig{Name: "x", Parameters: map[string]any{}}, th)
	at := tool.(*adkTool)

	_, err := at.Run(nilAgentContext{}, map[string]any{})
	if err != adktool.ErrConfirmationRejected {
		t.Errorf("err = %v, want ErrConfirmationRejected", err)
	}
}

// TestAdkTool_Run_NonMapArgs 特征化非 map 参数：json.Marshal 后透传 handler。
func TestAdkTool_Run_NonMapArgs(t *testing.T) {
	th := ToolHandler(func(_ context.Context, args json.RawMessage) *ToolResult {
		return &ToolResult{Success: true, Output: string(args)}
	})
	tool := NewAdkTool(AdkToolConfig{Name: "x", Parameters: map[string]any{}}, th)
	at := tool.(*adkTool)

	res, err := at.Run(nilAgentContext{}, "hello")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res["output"] != `"hello"` {
		t.Errorf("output = %v, want \"hello\"", res["output"])
	}
}
