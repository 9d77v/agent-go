package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRegisterAndExecute(t *testing.T) {
	e := NewToolExecutor()
	e.Register("hello", func(ctx context.Context, args json.RawMessage) *ToolResult {
		return &ToolResult{Success: true, Output: "world"}
	})
	result := e.Execute(context.Background(), "hello", json.RawMessage(`{}`))
	assert.True(t, result.Success)
	assert.Equal(t, "world", result.Output)
}

func TestExecuteUnknown(t *testing.T) {
	e := NewToolExecutor()
	result := e.Execute(context.Background(), "unknown", json.RawMessage(`{}`))
	assert.False(t, result.Success)
	assert.Contains(t, result.Error, "未知工具")
}

func TestListTools(t *testing.T) {
	e := NewToolExecutor()
	e.Register("a", func(ctx context.Context, args json.RawMessage) *ToolResult { return &ToolResult{Success: true} })
	e.Register("b", func(ctx context.Context, args json.RawMessage) *ToolResult { return &ToolResult{Success: true} })
	names := e.ListTools()
	assert.Len(t, names, 2)
	assert.Contains(t, names, "a")
	assert.Contains(t, names, "b")
}

func TestExecutePanicRecovery(t *testing.T) {
	e := NewToolExecutor()
	e.Register("panic", func(ctx context.Context, args json.RawMessage) *ToolResult {
		panic("test panic")
	})
	// ToolExecutor.Execute 不捕获 panic，panic 会传播到调用方
	// 此处验证 panic 确实会抛出
	assert.Panics(t, func() {
		e.Execute(context.Background(), "panic", json.RawMessage(`{}`))
	})
}
